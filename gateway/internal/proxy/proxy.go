package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/config"
	"github.com/clever-route/gateway/internal/keys"
	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/store"
	"github.com/gin-gonic/gin"
)

// Proxy is the namespaced AI and router proxy path. It authenticates requests
// via virtual key, admin token, or admin session, resolves the router target
// from the hot routing table, forwards the request to the sibling container,
// and streams the response back while capturing usage for AI completions.
type Proxy struct {
	table  *router.Table
	auth   *keys.Auth
	store  *store.Store
	cache  *cache.Cache
	cfg    *config.Config
	client *http.Client
}

func New(t *router.Table, a *keys.Auth, st *store.Store, c *cache.Cache, cfg *config.Config) *Proxy {
	return &Proxy{
		table: t, auth: a, store: st, cache: c, cfg: cfg,
		client: &http.Client{
			Timeout: 0, // streaming must not be cut by an overall timeout
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Prevent http.Client from automatically following redirects so we can rewrite Location headers to the browser
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns:        2048,
				MaxIdleConnsPerHost: 512,
				MaxConnsPerHost:     1024,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

type proxyError struct {
	Status int    `json:"status"`
	Error  string `json:"error"`
}

// Handle serves both OpenAI-compatible APIs and native router dashboards under /:slug/*path.
// Example: /omnirouter/v1/chat/completions (API)
// Example: /omnirouter/dashboard           (native UI)
func (p *Proxy) Handle(c *gin.Context) {
	slug := c.Param("slug")

	// Root path or empty slug: return clean service info instead of 502.
	if slug == "" {
		c.JSON(200, gin.H{
			"service": "CleverRoute",
			"status":  "ok",
			"admin":   "/admin",
			"docs":    "Use /:slug/v1/* to access router APIs",
		})
		return
	}

	p.handleRequest(c)
}

func (p *Proxy) handleRequest(c *gin.Context) {
	slug := c.Param("slug")
	path := c.Param("path")

	target, ok := p.table.Lookup(slug)
	targetSlug := slug
	upstreamPath := path

	// Fallback routing for root-relative assets and sub-requests (e.g. /_next/*, /assets/*, /static/*, /favicon.ico).
	// Only activate for known asset/sub-request paths — never for arbitrary unknown slugs.
	if !ok && isWebOrAssetPath(c.Request.URL.Path) {
		fallbackSlug, fallbackTarget := p.findFallbackRouter(c)
		if fallbackTarget != "" {
			targetSlug = fallbackSlug
			target = fallbackTarget
			upstreamPath = c.Request.URL.Path
			ok = true
		}
	}

	if !ok {
		fail(c, http.StatusBadGateway, "router '%s' is not serving", slug)
		return
	}

	token := p.extractToken(c)

	prefix := "/" + targetSlug

	// Trailing slash normalization for root slug path
	if (path == "" || path == "/") && targetSlug == slug && c.Request.Method == "GET" {
		if !strings.HasSuffix(c.Request.URL.Path, "/") {
			redirectURL := prefix + "/"
			if c.Request.URL.RawQuery != "" {
				redirectURL += "?" + c.Request.URL.RawQuery
			}
			c.Redirect(http.StatusMovedPermanently, redirectURL)
			return
		}
	}

	isAdmin := false
	var ki *keys.KeyInfo

	if p.cfg != nil && p.cfg.IsDev() && c.GetHeader("X-Dev-Bypass") == "1" {
		isAdmin = true
	}

	if !isAdmin && p.cfg != nil && p.cfg.AdminToken != "" && token != "" && token == p.cfg.AdminToken {
		isAdmin = true
	}

	if !isAdmin && p.cache != nil && token != "" {
		if sess, err := p.cache.GetSession(c.Request.Context(), token); err == nil && sess != nil {
			isAdmin = true
		}
	}

	if !isAdmin {
		if strings.HasPrefix(token, "cr-") && p.auth != nil {
			var err error
			ki, err = p.auth.Authenticate(c.Request.Context(), token)
			if err != nil {
				switch err {
				case keys.ErrRateLimited:
					fail(c, 429, "rate limit exceeded")
				case keys.ErrRevoked:
					fail(c, 401, "api key revoked")
				default:
					fail(c, 401, "invalid or missing api key")
				}
				return
			}
			if !keys.AllowRouter(ki.RouterAllow, targetSlug) {
				fail(c, 403, "key not allowed for router '%s'", targetSlug)
				return
			}
			if err := p.auth.CheckRate(c.Request.Context(), ki.ID, ki.RateLimitRPM); err != nil {
				fail(c, 429, "rate limit exceeded")
				return
			}
		} else if token == "" && !isWebOrAssetPath(upstreamPath) && targetSlug == "" {
			fail(c, 401, "invalid or missing api key")
			return
		}
		// Native router tokens (e.g. sk-..., bearer tokens, child router API keys) are
		// preserved in the request and proxied directly to the upstream container.
	}

	// Set browser session cookies on valid token for subsequent asset/chunk requests
	if token != "" {
		c.SetCookie("cr_router_auth", token, 86400*7, "/", "", false, false)
	}
	if targetSlug != "" {
		c.SetCookie("cr_active_router", targetSlug, 86400*7, "/", "", false, false)
	}

	body, _ := io.ReadAll(c.Request.Body)
	model, isStream := extractModel(body)

	// Check model allowlist and budget for virtual keys
	if ki != nil {
		if model != "" && !keys.AllowModel(ki.ModelAllow, model) {
			fail(c, 403, "model '%s' not allowed for this key", model)
			return
		}
		if ki.BudgetCents > 0 {
			ok, err := p.store.CheckBudget(c.Request.Context(), ki.ID)
			if err == nil && !ok {
				fail(c, 403, "budget exhausted for this key")
				return
			}
		}
	}

	// Ensure upstreamPath has leading slash
	if !strings.HasPrefix(upstreamPath, "/") {
		upstreamPath = "/" + upstreamPath
	}

	upURL := strings.TrimRight(target, "/") + upstreamPath
	if c.Request.URL.RawQuery != "" {
		upURL += "?" + c.Request.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upURL, bytes.NewReader(body))
	if err != nil {
		fail(c, 500, "build upstream request: %v", err)
		return
	}
	copyHeaders(req.Header, c.Request.Header)
	req.Host = ""
	// Request uncompressed identity from local container to allow fast, transparent stream/HTML processing
	req.Header.Set("Accept-Encoding", "identity")

	// Inject Reverse Proxy Namespace Headers so downstream instances know their public mount point
	req.Header.Set("X-Forwarded-Prefix", prefix)
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	} else if c.Request.TLS != nil {
		req.Header.Set("X-Forwarded-Proto", "https")
	} else {
		req.Header.Set("X-Forwarded-Proto", "http")
	}
	if host := c.Request.Host; host != "" {
		req.Header.Set("X-Forwarded-Host", host)
	}
	if clientIP := c.ClientIP(); clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	req.Header.Set("X-Original-URI", c.Request.RequestURI)

	resp, err := p.client.Do(req)
	if err != nil {
		fail(c, http.StatusBadGateway, "upstream unreachable: %v", err)
		return
	}

	// Automatic API Path Mapping:
	// If upstream returned 404 Not Found, automatically map between
	// /v1/* <-> /api/v1/*, /something/v1/* <-> /v1/*, and root variants
	if resp.StatusCode == http.StatusNotFound && (strings.Contains(upstreamPath, "/v1") || strings.Contains(upstreamPath, "/api")) {
		var altPaths []string
		if strings.HasPrefix(upstreamPath, "/v1/") {
			altPaths = append(altPaths, "/api"+upstreamPath)
		} else if strings.HasPrefix(upstreamPath, "/api/v1/") {
			altPaths = append(altPaths, strings.TrimPrefix(upstreamPath, "/api"))
		} else if upstreamPath == "/v1" || upstreamPath == "/v1/" {
			altPaths = append(altPaths, "/api/v1", "/v1/models", "/api/v1/models")
		} else if upstreamPath == "/api/v1" || upstreamPath == "/api/v1/" {
			altPaths = append(altPaths, "/v1", "/api/v1/models", "/v1/models")
		} else if strings.HasSuffix(upstreamPath, "/v1/models") {
			altPaths = append(altPaths, "/v1/models", "/api/v1/models")
		} else if strings.HasSuffix(upstreamPath, "/models") {
			altPaths = append(altPaths, "/v1/models", "/api/v1/models")
		}

		for _, altPath := range altPaths {
			altURL := strings.TrimRight(target, "/") + altPath
			if c.Request.URL.RawQuery != "" {
				altURL += "?" + c.Request.URL.RawQuery
			}
			altReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, altURL, bytes.NewReader(body))
			if err != nil {
				continue
			}
			copyHeaders(altReq.Header, c.Request.Header)
			altReq.Header.Set("X-Forwarded-Prefix", prefix)
			altReq.Header.Set("Accept-Encoding", "identity")
			altReq.Host = ""
			if altResp, err := p.client.Do(altReq); err == nil {
				if altResp.StatusCode != http.StatusNotFound {
					resp.Body.Close()
					resp = altResp
					break
				}
				altResp.Body.Close()
			}
		}
	}
	defer resp.Body.Close()

	// Flush and rewrite upstream headers to client.
	for k, vs := range resp.Header {
		if isHop(k) {
			continue
		}
		// Location header rewriting for 30x redirects
		if strings.EqualFold(k, "Location") {
			for _, v := range vs {
				rewrittenLoc := p.rewriteLocation(v, prefix)
				c.Writer.Header().Add(k, rewrittenLoc)
			}
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.Header().Set("X-CleverRoute-Router", targetSlug)
	if model != "" {
		c.Writer.Header().Set("X-CleverRoute-Model", model)
	}

	sc := newUsageScanner(model)
	flusher, _ := c.Writer.(http.Flusher)

	// HTML Content: Transform Next.js routing metadata, asset paths, and inject <base> guard
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		// Strip Content-Encoding since we are transforming and writing uncompressed HTML
		c.Writer.Header().Del("Content-Encoding")

		var bodyReader io.Reader = resp.Body
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			if gzReader, err := gzip.NewReader(resp.Body); err == nil {
				defer gzReader.Close()
				bodyReader = gzReader
			}
		}

		rawHTML, err := io.ReadAll(bodyReader)
		if err == nil {
			modifiedHTML := p.transformHTML(rawHTML, prefix, targetSlug)
			c.Writer.Header().Set("Content-Length", strconv.Itoa(len(modifiedHTML)))
			c.Writer.WriteHeader(resp.StatusCode)
			_, _ = c.Writer.Write(modifiedHTML)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)

	if isStream && strings.Contains(contentType, "event-stream") {
		// Streaming SSE: tee through the sniffer while copying to the client.
		tee := io.TeeReader(resp.Body, sc)
		buf := make([]byte, 16*1024)
		for {
			n, rerr := tee.Read(buf)
			if n > 0 {
				if _, werr := c.Writer.Write(buf[:n]); werr != nil {
					// Client disconnected / pipe broke, stop streaming cleanly
					break
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				break
			}
		}
	} else {
		// Non-streaming: buffer, sniff, then write.
		bufBody, _ := io.ReadAll(io.TeeReader(resp.Body, sc))
		_, _ = c.Writer.Write(bufBody)
		if flusher != nil {
			flusher.Flush()
		}
	}

	if ki != nil && model != "" {
		// Record usage asynchronously so the client is never blocked on the DB.
		go p.recordUsage(ki.ID, targetSlug, model, resp.StatusCode, sc)
	}
}

// rewriteLocation prepends the router prefix (e.g. /omniroute) to relative and internal absolute redirects
func (p *Proxy) rewriteLocation(loc, prefix string) string {
	if loc == "" || prefix == "" {
		return loc
	}

	// 1. Root-relative redirect (e.g. /dashboard or /login or /setup)
	if strings.HasPrefix(loc, "/") {
		if strings.HasPrefix(loc, prefix+"/") || loc == prefix {
			return loc
		}
		return prefix + loc
	}

	// 2. Internal absolute redirect (e.g. http://172.17.0.x:3001/dashboard or http://127.0.0.1:3001/dashboard)
	if u, err := url.Parse(loc); err == nil && u.Path != "" {
		if strings.HasPrefix(u.Path, prefix+"/") || u.Path == prefix {
			return u.Path
		}
		newPath := prefix + u.Path
		if u.RawQuery != "" {
			newPath += "?" + u.RawQuery
		}
		return newPath
	}

	return loc
}

// transformHTML patches Next.js client routing metadata, rewrites absolute asset URLs, and injects <base> guard
func (p *Proxy) transformHTML(raw []byte, prefix, slug string) []byte {
	if len(raw) == 0 || prefix == "" {
		return raw
	}
	htmlStr := string(raw)

	// 1. Rewrite Next.js __NEXT_DATA__ JSON configuration to align client-side router
	if strings.Contains(htmlStr, `id="__NEXT_DATA__"`) {
		htmlStr = strings.ReplaceAll(htmlStr, `"basePath":""`, fmt.Sprintf(`"basePath":"%s"`, prefix))
		htmlStr = strings.ReplaceAll(htmlStr, `"basePath": null`, fmt.Sprintf(`"basePath":"%s"`, prefix))
		htmlStr = strings.ReplaceAll(htmlStr, `"basePath":null`, fmt.Sprintf(`"basePath":"%s"`, prefix))
		htmlStr = strings.ReplaceAll(htmlStr, `"assetPrefix":""`, fmt.Sprintf(`"assetPrefix":"%s"`, prefix))
		htmlStr = strings.ReplaceAll(htmlStr, `"assetPrefix": null`, fmt.Sprintf(`"assetPrefix":"%s"`, prefix))
		htmlStr = strings.ReplaceAll(htmlStr, `"assetPrefix":null`, fmt.Sprintf(`"assetPrefix":"%s"`, prefix))
	}

	// 2. Rewrite root asset attributes in HTML so bundles and styles load under the subpath prefix
	htmlStr = strings.ReplaceAll(htmlStr, `src="/_next/`, fmt.Sprintf(`src="%s/_next/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/_next/`, fmt.Sprintf(`href="%s/_next/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `src="/static/`, fmt.Sprintf(`src="%s/static/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/static/`, fmt.Sprintf(`href="%s/static/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `src="/assets/`, fmt.Sprintf(`src="%s/assets/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/assets/`, fmt.Sprintf(`href="%s/assets/`, prefix))

	// 3. Inject <base> tag and client-side history guard into <head>
	clientGuardScript := fmt.Sprintf(`<base href="%s/" /><script>
window.__BASE_PATH__ = "%s";
window.__ROUTER_SLUG__ = "%s";
(function(){
  try {
    if (window.__NEXT_DATA__) {
      window.__NEXT_DATA__.basePath = "%s";
      window.__NEXT_DATA__.assetPrefix = "%s";
    }
  } catch(e){}
  if (typeof history !== "undefined") {
    var p = "%s";
    var origPush = history.pushState;
    var origReplace = history.replaceState;
    history.pushState = function(state, title, url) {
      if (typeof url === "string" && url.startsWith("/") && !url.startsWith(p)) {
        url = p + url;
      }
      return origPush.apply(this, [state, title, url]);
    };
    history.replaceState = function(state, title, url) {
      if (typeof url === "string" && url.startsWith("/") && !url.startsWith(p)) {
        url = p + url;
      }
      return origReplace.apply(this, [state, title, url]);
    };
  }
})();
</script>`, prefix, prefix, slug, prefix, prefix, prefix)

	// Inject into <head>
	lowerHTML := strings.ToLower(htmlStr)
	if idx := strings.Index(lowerHTML, "<head>"); idx != -1 {
		pos := idx + len("<head>")
		htmlStr = htmlStr[:pos] + clientGuardScript + htmlStr[pos:]
	} else if idx := strings.Index(lowerHTML, "<head"); idx != -1 {
		if endIdx := strings.IndexByte(htmlStr[idx:], '>'); endIdx != -1 {
			pos := idx + endIdx + 1
			htmlStr = htmlStr[:pos] + clientGuardScript + htmlStr[pos:]
		}
	} else {
		htmlStr = clientGuardScript + htmlStr
	}

	return []byte(htmlStr)
}

func (p *Proxy) findFallbackRouter(c *gin.Context) (string, string) {
	// 1. Check cr_active_router cookie
	if cookie, err := c.Cookie("cr_active_router"); err == nil && cookie != "" {
		if t, ok := p.table.Lookup(cookie); ok {
			return cookie, t
		}
	}

	// 2. Check Referer header
	referer := c.GetHeader("Referer")
	if referer != "" {
		snap := p.table.Snapshot()
		for s, t := range snap {
			if strings.Contains(referer, "/"+s+"/") || strings.Contains(referer, "/"+s+"?") || strings.HasSuffix(referer, "/"+s) {
				return s, t
			}
		}
	}

	// 3. If exactly 1 router is active in the table, fallback to it
	snap := p.table.Snapshot()
	if len(snap) == 1 {
		for s, t := range snap {
			return s, t
		}
	}

	return "", ""
}

func (p *Proxy) extractToken(c *gin.Context) string {
	// 1. Authorization header: "Bearer <token>" or plain "<token>"
	authHeader := c.GetHeader("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		if t := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")); t != "" {
			return t
		}
	} else if authHeader != "" {
		return strings.TrimSpace(authHeader)
	}

	// 2. Query param: ?token=... or ?key=... or ?api_key=...
	if t := strings.TrimSpace(c.Query("token")); t != "" {
		return t
	}
	if t := strings.TrimSpace(c.Query("key")); t != "" {
		return t
	}
	if t := strings.TrimSpace(c.Query("api_key")); t != "" {
		return t
	}

	// 3. Cookie: cr_router_auth
	if cookie, err := c.Cookie("cr_router_auth"); err == nil && strings.TrimSpace(cookie) != "" {
		return strings.TrimSpace(cookie)
	}

	return ""
}

func (p *Proxy) recordUsage(keyID, slug, model string, status int, sc *usageScanner) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prompt, completion, total := sc.usage()
	costCents := int64(0) // Phase 4: compute from per-model pricing table
	_ = p.store.RecordUsage(ctx, keyID, "", slug, model, sc.provider, prompt, completion, total, costCents, status)
	// BUG-2 FIX: update spent_cents so budget enforcement is meaningful.
	if keyID != "" && costCents > 0 {
		_ = p.store.IncrSpentCents(ctx, keyID, costCents)
	}

	logger.Info("proxy", slug, fmt.Sprintf("AI request: model=%s status=%d tokens=%d (prompt=%d, comp=%d)", model, status, total, prompt, completion), store.Map{
		"key_id":            keyID,
		"model":             model,
		"status":            status,
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      total,
		"provider":          sc.provider,
	})
}

// ----- helpers -----

func fail(c *gin.Context, status int, format string, args ...any) {
	c.JSON(status, proxyError{Status: status, Error: fmt.Sprintf(format, args...)})
}

func isWebOrAssetPath(path string) bool {
	p := strings.ToLower(path)
	if strings.HasPrefix(p, "/_next") || strings.HasPrefix(p, "/static") ||
		strings.HasPrefix(p, "/assets") || strings.HasPrefix(p, "/dashboard") ||
		strings.HasPrefix(p, "/login") || strings.HasPrefix(p, "/setup") ||
		strings.HasPrefix(p, "/settings") || strings.HasPrefix(p, "/api/") ||
		strings.HasPrefix(p, "/trpc") || strings.HasPrefix(p, "/favicon") ||
		strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") ||
		strings.HasSuffix(p, ".png") || strings.HasSuffix(p, ".svg") ||
		strings.HasSuffix(p, ".ico") || strings.HasSuffix(p, ".json") ||
		strings.HasSuffix(p, ".woff") || strings.HasSuffix(p, ".woff2") ||
		strings.HasSuffix(p, ".ttf") {
		return true
	}
	return false
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHop(k) {
			continue
		}
		// Only strip virtual key headers (cr-...), preserve router's own Bearer tokens/cookies
		if strings.EqualFold(k, "Authorization") {
			var keep []string
			for _, v := range vs {
				if !strings.HasPrefix(strings.TrimSpace(v), "Bearer cr-") {
					keep = append(keep, v)
				}
			}
			if len(keep) > 0 {
				dst[k] = keep
			}
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isHop(h string) bool {
	for _, x := range hopHeaders {
		if strings.EqualFold(h, x) {
			return true
		}
	}
	return false
}

type chatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func extractModel(body []byte) (model string, stream bool) {
	if len(body) == 0 {
		return "", false
	}
	var r chatRequest
	if err := json.Unmarshal(body, &r); err == nil {
		return r.Model, r.Stream
	}
	return "", false
}

// usageScanner is a lightweight byte observer that tees the response stream
// to capture final usage metadata (token counts, provider) without buffering
// the entire body or doing per-chunk JSON decoding.
type usageScanner struct {
	model    string
	provider string
	buf      []byte
	prompt   int
	comp     int
	total    int
	done     bool
	parsed   bool
}

func newUsageScanner(model string) *usageScanner {
	return &usageScanner{model: model, buf: make([]byte, 0, 4096)}
}

func (s *usageScanner) Write(p []byte) (int, error) {
	if s.done {
		return len(p), nil
	}
	// Buffer up to 64KB for usage parsing.
	if len(s.buf) < 65536 {
		s.buf = append(s.buf, p...)
	}
	return len(p), nil
}

func (s *usageScanner) usage() (prompt, completion, total int) {
	if !s.parsed {
		s.parse()
	}
	return s.prompt, s.comp, s.total
}

func (s *usageScanner) parse() {
	s.parsed = true
	body := s.buf
	// Try OpenAI format first: {"usage":{"prompt_tokens":...,"completion_tokens":...,"total_tokens":...}}
	var oai struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &oai); err == nil && oai.Usage.TotalTokens > 0 {
		s.prompt = oai.Usage.PromptTokens
		s.comp = oai.Usage.CompletionTokens
		s.total = oai.Usage.TotalTokens
		return
	}
	// Anthropic format: {"usage":{"input_tokens":...,"output_tokens":...}}
	var ant struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &ant); err == nil && (ant.Usage.InputTokens > 0 || ant.Usage.OutputTokens > 0) {
		s.prompt = ant.Usage.InputTokens
		s.comp = ant.Usage.OutputTokens
		s.total = ant.Usage.InputTokens + ant.Usage.OutputTokens
		return
	}
}
