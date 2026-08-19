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
		// If an active router session exists (from /:slug/open), forward root requests to the active router UI
		if cookie, err := c.Cookie("cr_active_router"); err == nil && cookie != "" {
			if _, found := p.table.Lookup(cookie); found {
				p.handleRequest(c)
				return
			}
		}
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
	isRootProxy := false

	// 0. Subdomain / Host-header routing (e.g. omnirouter.yourdomain.com -> router: omnirouter)
	host := c.Request.Host
	if i := strings.Index(host, ":"); i > 0 {
		host = host[:i]
	}
	if hostParts := strings.Split(host, "."); len(hostParts) > 2 {
		subdomain := strings.ToLower(hostParts[0])
		if subdomain != "admin" && subdomain != "api" && subdomain != "app" && !strings.HasPrefix(subdomain, "app-") {
			if t, found := p.table.Lookup(subdomain); found {
				targetSlug = subdomain
				target = t
				upstreamPath = c.Request.URL.Path
				ok = true
				isRootProxy = true
			}
		}
	}

	// 1. Fallback routing for root-relative requests (e.g. /home, /providers, /login, /dashboard, /_next/*, /assets/*, etc.)
	if !ok {
		fallbackSlug, fallbackTarget := p.findFallbackRouter(c)
		if fallbackTarget != "" {
			targetSlug = fallbackSlug
			target = fallbackTarget
			upstreamPath = c.Request.URL.Path
			ok = true
			isRootProxy = true
		}
	}

	// 2. Direct PostgreSQL fallback if in-memory table has not synced yet
	if !ok && slug != "" {
		if r, err := p.store.GetRouterBySlug(c.Request.Context(), slug); err == nil && r != nil && r.TargetAddr != "" {
			targetSlug = r.Slug
			target = r.TargetAddr
			ok = true
			p.table.Set(r.Slug, r.TargetAddr)
		}
	}

	if !ok {
		fail(c, http.StatusBadGateway, "router '%s' is not serving", slug)
		return
	}

	if isRootProxy && c.Request.Method == "GET" && !strings.HasPrefix(c.Request.URL.Path, "/v1") && !strings.HasPrefix(c.Request.URL.Path, "/api") {
		low := strings.ToLower(targetSlug)
		if strings.Contains(low, "portkey") {
			p.servePortkeyDashboard(c, targetSlug, target)
			return
		}
		if c.Request.URL.Path == "/dashboard" || c.Request.URL.Path == "/dashboard/" {
			if strings.Contains(low, "freellm") {
				dest := "/"
				if c.Request.URL.RawQuery != "" {
					dest += "?" + c.Request.URL.RawQuery
				}
				c.Redirect(http.StatusFound, dest)
				return
			}
		}
	}

	// 2. Direct Web UI Activation & Redirection (for single-domain mode)
	// When user visits /:slug/open, /:slug/dashboard, /:slug/ui, or /:slug/login directly in browser:
	// Set the active router cookie and redirect to root /dashboard or /login for 100% native Next.js App Router execution.
	if !isRootProxy && c.Request.Method == "GET" && !strings.HasPrefix(upstreamPath, "/v1") && !strings.HasPrefix(upstreamPath, "/api") {
		cleanPath := strings.Trim(upstreamPath, "/")
		low := strings.ToLower(targetSlug)

		// Serve interactive Portkey AI Gateway Web Dashboard if requested directly in browser
		if strings.Contains(low, "portkey") && (cleanPath == "open" || cleanPath == "ui" || cleanPath == "dashboard" || cleanPath == "" || cleanPath == "healthz") {
			p.servePortkeyDashboard(c, targetSlug, target)
			return
		}

		if cleanPath == "open" || cleanPath == "dashboard" || cleanPath == "login" || cleanPath == "" {
			c.SetCookie("cr_active_router", targetSlug, 86400*7, "/", "", false, false)
			dest := "/dashboard"
			if strings.Contains(low, "bifrost") {
				dest = "/workspace/logs"
			} else if strings.Contains(low, "litellm") {
				dest = fmt.Sprintf("/%s/ui/", targetSlug)
			} else if strings.Contains(low, "freellm") {
				if cleanPath == "login" {
					dest = "/login"
				} else {
					dest = "/"
				}
			} else if cleanPath == "login" {
				dest = "/login"
			}
			if c.Request.URL.RawQuery != "" {
				if strings.Contains(dest, "?") {
					dest += "&" + c.Request.URL.RawQuery
				} else {
					dest += "?" + c.Request.URL.RawQuery
				}
			}
			c.Redirect(http.StatusFound, dest)
			return
		}
	}

	token := p.extractToken(c)

	prefix := "/" + targetSlug
	if isRootProxy {
		prefix = ""
	}

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

	sc := newUsageScanner(model)
	flusher, _ := c.Writer.(http.Flusher)

	// Detect HTML early so we can skip encoding-related headers during copy.
	// When we transform HTML, the original Content-Length and Content-Encoding
	// become stale. We must NOT forward them or edge proxies (Sozu/Clever Cloud)
	// will create a mismatch between the declared and actual body encoding.
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	isHTML := strings.Contains(contentType, "text/html")

	// Flush and rewrite upstream headers to client.
	for k, vs := range resp.Header {
		if isHop(k) {
			continue
		}
		// For HTML responses we rewrite the body, so skip headers that describe
		// the upstream body encoding — we'll write our own uncompressed body.
		if isHTML {
			kl := strings.ToLower(k)
			if kl == "content-encoding" || kl == "content-length" || kl == "transfer-encoding" {
				continue
			}
		}
		// Location header rewriting for 30x redirects
		if strings.EqualFold(k, "Location") {
			for _, v := range vs {
				rewrittenLoc := p.rewriteLocation(v, prefix)
				c.Writer.Header().Add(k, rewrittenLoc)
			}
			continue
		}
		// Rewrite Link preload headers (e.g. </_next/static/media/font.woff2>; rel=preload)
		// to include the subpath prefix so preloaded resources are found.
		if isHTML && strings.EqualFold(k, "Link") {
			for _, v := range vs {
				// Rewrite root-relative paths inside angle brackets: </path> → </prefix/path>
				rewritten := v
				if strings.Contains(rewritten, "</") {
					rewritten = strings.ReplaceAll(rewritten, "</_next/", fmt.Sprintf("<%s/_next/", prefix))
					rewritten = strings.ReplaceAll(rewritten, "</static/", fmt.Sprintf("<%s/static/", prefix))
					rewritten = strings.ReplaceAll(rewritten, "</assets/", fmt.Sprintf("<%s/assets/", prefix))
					rewritten = strings.ReplaceAll(rewritten, "</favicon", fmt.Sprintf("<%s/favicon", prefix))
					rewritten = strings.ReplaceAll(rewritten, "</manifest", fmt.Sprintf("<%s/manifest", prefix))
				}
				c.Writer.Header().Add(k, rewritten)
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

	// HTML Content: Transform Next.js routing metadata, asset paths, and inject <base> guard
	if isHTML {
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
			// Do NOT set Content-Length here. Edge proxies (Sozu) may re-compress
			// the response, which would invalidate any Content-Length we set.
			// Let the HTTP stack use chunked transfer encoding instead.
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

	// Rewrite ALL Next.js App Router RSC chunk references and JS string literals (both unescaped and escaped JSON)
	// Next.js 14 App Router embeds 100+ client component chunk paths in self.__next_f payloads as \"/_next/...\"
	htmlStr = strings.ReplaceAll(htmlStr, `\"/_next/`, fmt.Sprintf(`\"%s/_next/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"/_next/`, fmt.Sprintf(`"%s/_next/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `'/_next/`, fmt.Sprintf(`'%s/_next/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"/static/`, fmt.Sprintf(`\"%s/static/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"/static/`, fmt.Sprintf(`"%s/static/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `'/_static/`, fmt.Sprintf(`'%s/static/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"/assets/`, fmt.Sprintf(`\"%s/assets/`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"/assets/`, fmt.Sprintf(`"%s/assets/`, prefix))

	// Rewrite common root-relative PWA/meta resources (manifest, icons, favicons, etc.)
	htmlStr = strings.ReplaceAll(htmlStr, `href="/manifest.webmanifest"`, fmt.Sprintf(`href="%s/manifest.webmanifest"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/manifest.json"`, fmt.Sprintf(`href="%s/manifest.json"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/favicon.ico"`, fmt.Sprintf(`href="%s/favicon.ico"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/favicon.svg"`, fmt.Sprintf(`href="%s/favicon.svg"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/icon-`, fmt.Sprintf(`href="%s/icon-`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `href="/apple-touch-icon`, fmt.Sprintf(`href="%s/apple-touch-icon`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `src="/favicon`, fmt.Sprintf(`src="%s/favicon`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `src="/icon-`, fmt.Sprintf(`src="%s/icon-`, prefix))

	// RSC (React Server Components) JSON payload format (both unescaped and escaped JSON)
	htmlStr = strings.ReplaceAll(htmlStr, `"href":"/manifest.webmanifest"`, fmt.Sprintf(`"href":"%s/manifest.webmanifest"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"href":"/manifest.json"`, fmt.Sprintf(`"href":"%s/manifest.json"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"href":"/favicon.ico"`, fmt.Sprintf(`"href":"%s/favicon.ico"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"href":"/favicon.svg"`, fmt.Sprintf(`"href":"%s/favicon.svg"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"href":"/icon-`, fmt.Sprintf(`"href":"%s/icon-`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"href":"/apple-touch-icon`, fmt.Sprintf(`"href":"%s/apple-touch-icon`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"src":"/favicon`, fmt.Sprintf(`"src":"%s/favicon`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `"src":"/icon-`, fmt.Sprintf(`"src":"%s/icon-`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"href\":\"/manifest.webmanifest\"`, fmt.Sprintf(`\"href\":\"%s/manifest.webmanifest\"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"href\":\"/manifest.json\"`, fmt.Sprintf(`\"href\":\"%s/manifest.json\"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"href\":\"/favicon.ico\"`, fmt.Sprintf(`\"href\":\"%s/favicon.ico\"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"href\":\"/favicon.svg\"`, fmt.Sprintf(`\"href\":\"%s/favicon.svg\"`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"href\":\"/icon-`, fmt.Sprintf(`\"href\":\"%s/icon-`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"href\":\"/apple-touch-icon`, fmt.Sprintf(`\"href\":\"%s/apple-touch-icon`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"src\":\"/favicon`, fmt.Sprintf(`\"src\":\"%s/favicon`, prefix))
	htmlStr = strings.ReplaceAll(htmlStr, `\"src\":\"/icon-`, fmt.Sprintf(`\"src\":\"%s/icon-`, prefix))

	// 3. Inject client-side routing & fetch guard into <head>
	clientGuardScript := fmt.Sprintf(`<script>
window.__BASE_PATH__ = "%s";
window.__ROUTER_SLUG__ = "%s";
(function(){
  try {
    if (window.__NEXT_DATA__) {
      window.__NEXT_DATA__.basePath = "%s";
      window.__NEXT_DATA__.assetPrefix = "%s";
    }
  } catch(e){}
  var p = "%s";
  if (typeof history !== "undefined") {
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
  if (typeof window.fetch !== "undefined") {
    var origFetch = window.fetch;
    window.fetch = function(input, init) {
      if (typeof input === "string") {
        if (input.startsWith("/") && !input.startsWith(p + "/") && !input.startsWith("/admin")) {
          input = p + input;
        }
      } else if (input instanceof Request) {
        try {
          var u = new URL(input.url);
          if (u.origin === window.location.origin && u.pathname.startsWith("/") && !u.pathname.startsWith(p + "/") && !u.pathname.startsWith("/admin")) {
            input = new Request(p + u.pathname + u.search + u.hash, input);
          }
        } catch(e){}
      }
      return origFetch.apply(this, [input, init]);
    };
  }
})();
</script>`, prefix, slug, prefix, prefix, prefix)

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
		strings.HasPrefix(p, "/home") || strings.HasPrefix(p, "/providers") ||
		strings.HasPrefix(p, "/models") || strings.HasPrefix(p, "/usage") ||
		strings.HasPrefix(p, "/keys") || strings.HasPrefix(p, "/audit") ||
		strings.HasPrefix(p, "/logs") || strings.HasPrefix(p, "/playground") ||
		strings.HasPrefix(p, "/chaos") || strings.HasPrefix(p, "/evals") ||
		strings.HasPrefix(p, "/settings") || strings.HasPrefix(p, "/account") ||
		strings.HasPrefix(p, "/login") || strings.HasPrefix(p, "/setup") ||
		strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/trpc") ||
		strings.HasPrefix(p, "/favicon") || strings.HasPrefix(p, "/manifest") ||
		strings.HasPrefix(p, "/icon-") || strings.HasPrefix(p, "/apple-touch-icon") ||
		strings.HasPrefix(p, "/robots") ||
		strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") ||
		strings.HasSuffix(p, ".png") || strings.HasSuffix(p, ".svg") ||
		strings.HasSuffix(p, ".ico") || strings.HasSuffix(p, ".json") ||
		strings.HasSuffix(p, ".woff") || strings.HasSuffix(p, ".woff2") ||
		strings.HasSuffix(p, ".ttf") || strings.HasSuffix(p, ".webmanifest") ||
		strings.HasSuffix(p, ".xml") || strings.HasSuffix(p, ".txt") ||
		strings.HasSuffix(p, ".map") || strings.HasSuffix(p, ".jpg") ||
		strings.HasSuffix(p, ".jpeg") || strings.HasSuffix(p, ".gif") ||
		strings.HasSuffix(p, ".webp") {
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

func (p *Proxy) servePortkeyDashboard(c *gin.Context, slug, targetAddr string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.String(http.StatusOK, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Portkey AI Gateway — Interactive Web Console</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #090d16;
      --card-bg: #111827;
      --card-border: #1f293d;
      --accent: #6366f1;
      --accent-hover: #4f46e5;
      --text: #f3f4f6;
      --text-muted: #9ca3af;
      --text-dim: #6b7280;
      --green: #10b981;
      --amber: #f59e0b;
      --red: #ef4444;
      --code-bg: #0b0f19;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
      background-color: var(--bg);
      color: var(--text);
      min-height: 100vh;
      display: flex;
      flex-direction: column;
    }
    header {
      background: rgba(17, 24, 39, 0.85);
      backdrop-filter: blur(12px);
      border-bottom: 1px solid var(--card-border);
      padding: 1rem 2rem;
      display: flex;
      align-items: center;
      justify-content: space-between;
      position: sticky;
      top: 0;
      z-index: 50;
    }
    .brand {
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }
    .logo-badge {
      background: linear-gradient(135deg, #6366f1 0%, #a855f7 100%);
      color: white;
      font-weight: 800;
      font-size: 1.1rem;
      width: 36px;
      height: 36px;
      display: grid;
      place-items: center;
      border-radius: 10px;
      box-shadow: 0 4px 12px rgba(99, 102, 241, 0.35);
    }
    .brand-title {
      font-size: 1.1rem;
      font-weight: 700;
      letter-spacing: -0.02em;
    }
    .status-pill {
      display: inline-flex;
      align-items: center;
      gap: 0.4rem;
      padding: 0.25rem 0.75rem;
      border-radius: 9999px;
      font-size: 0.75rem;
      font-weight: 600;
      background: rgba(16, 185, 129, 0.12);
      color: #34d399;
      border: 1px solid rgba(16, 185, 129, 0.3);
    }
    .status-dot {
      width: 7px;
      height: 7px;
      background: #10b981;
      border-radius: 50%;
      box-shadow: 0 0 8px #10b981;
    }
    .nav-links {
      display: flex;
      align-items: center;
      gap: 1rem;
    }
    .nav-btn {
      color: var(--text-muted);
      text-decoration: none;
      font-size: 0.85rem;
      font-weight: 500;
      padding: 0.4rem 0.85rem;
      border-radius: 6px;
      transition: all 0.15s;
    }
    .nav-btn:hover {
      color: var(--text);
      background: rgba(255, 255, 255, 0.05);
    }
    .nav-btn.primary {
      background: var(--accent);
      color: white;
    }
    .nav-btn.primary:hover {
      background: var(--accent-hover);
    }
    main {
      max-width: 1200px;
      width: 100%;
      margin: 2rem auto;
      padding: 0 1.5rem;
      flex: 1;
    }
    .banner {
      background: linear-gradient(135deg, rgba(99, 102, 241, 0.1) 0%, rgba(168, 85, 247, 0.05) 100%);
      border: 1px solid rgba(99, 102, 241, 0.25);
      border-radius: 12px;
      padding: 1.25rem 1.5rem;
      margin-bottom: 2rem;
      display: flex;
      align-items: center;
      justify-content: space-between;
      flex-wrap: wrap;
      gap: 1rem;
    }
    .banner-info h2 {
      font-size: 1.1rem;
      font-weight: 700;
      margin-bottom: 0.25rem;
    }
    .banner-info p {
      font-size: 0.85rem;
      color: var(--text-muted);
    }
    .endpoint-box {
      background: var(--code-bg);
      border: 1px solid var(--card-border);
      padding: 0.5rem 0.85rem;
      border-radius: 8px;
      font-family: 'JetBrains Mono', monospace;
      font-size: 0.8rem;
      color: #818cf8;
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }
    .tabs {
      display: flex;
      gap: 0.5rem;
      border-bottom: 1px solid var(--card-border);
      margin-bottom: 1.5rem;
    }
    .tab-btn {
      background: transparent;
      border: none;
      color: var(--text-muted);
      font-family: inherit;
      font-size: 0.9rem;
      font-weight: 600;
      padding: 0.75rem 1.25rem;
      cursor: pointer;
      border-bottom: 2px solid transparent;
      transition: all 0.2s;
    }
    .tab-btn:hover {
      color: var(--text);
    }
    .tab-btn.active {
      color: var(--accent);
      border-bottom-color: var(--accent);
    }
    .tab-content {
      display: none;
    }
    .tab-content.active {
      display: block;
    }
    .grid-2 {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 1.5rem;
    }
    @media (max-width: 850px) {
      .grid-2 { grid-template-columns: 1fr; }
    }
    .card {
      background: var(--card-bg);
      border: 1px solid var(--card-border);
      border-radius: 12px;
      padding: 1.5rem;
    }
    .card-title {
      font-size: 1rem;
      font-weight: 700;
      margin-bottom: 1rem;
      display: flex;
      align-items: center;
      gap: 0.5rem;
    }
    .form-group {
      margin-bottom: 1rem;
    }
    label {
      display: block;
      font-size: 0.75rem;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--text-muted);
      margin-bottom: 0.4rem;
    }
    input, select, textarea {
      width: 100%;
      background: var(--code-bg);
      border: 1px solid var(--card-border);
      color: var(--text);
      font-family: inherit;
      font-size: 0.85rem;
      padding: 0.6rem 0.85rem;
      border-radius: 8px;
      outline: none;
      transition: border-color 0.15s;
    }
    input:focus, select:focus, textarea:focus {
      border-color: var(--accent);
    }
    textarea {
      font-family: 'JetBrains Mono', monospace;
      min-height: 100px;
      resize: vertical;
    }
    .btn {
      background: var(--accent);
      color: white;
      border: none;
      font-family: inherit;
      font-size: 0.85rem;
      font-weight: 600;
      padding: 0.65rem 1.25rem;
      border-radius: 8px;
      cursor: pointer;
      display: inline-flex;
      align-items: center;
      gap: 0.5rem;
      transition: background 0.15s;
      width: 100%;
      justify-content: center;
    }
    .btn:hover {
      background: var(--accent-hover);
    }
    .btn:disabled {
      opacity: 0.6;
      cursor: not-allowed;
    }
    .output-area {
      background: var(--code-bg);
      border: 1px solid var(--card-border);
      border-radius: 8px;
      padding: 1rem;
      font-family: 'JetBrains Mono', monospace;
      font-size: 0.8rem;
      color: #d1d5db;
      min-height: 280px;
      max-height: 480px;
      overflow-y: auto;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .metric-tag {
      font-size: 0.75rem;
      padding: 0.2rem 0.5rem;
      border-radius: 4px;
      font-family: 'JetBrains Mono', monospace;
      font-weight: 600;
    }
    .metric-success { background: rgba(16, 185, 129, 0.15); color: #34d399; }
    .metric-error { background: rgba(239, 68, 68, 0.15); color: #f87171; }
    .code-block {
      background: var(--code-bg);
      border: 1px solid var(--card-border);
      border-radius: 8px;
      padding: 1rem;
      font-family: 'JetBrains Mono', monospace;
      font-size: 0.8rem;
      color: #e2e8f0;
      position: relative;
      margin-top: 0.5rem;
      white-space: pre;
      overflow-x: auto;
    }
    .copy-btn {
      position: absolute;
      top: 0.5rem;
      right: 0.5rem;
      background: rgba(255, 255, 255, 0.1);
      border: 1px solid rgba(255, 255, 255, 0.15);
      color: var(--text-muted);
      font-size: 0.7rem;
      padding: 0.25rem 0.5rem;
      border-radius: 4px;
      cursor: pointer;
    }
    .copy-btn:hover { background: rgba(255, 255, 255, 0.2); color: var(--text); }
  </style>
</head>
<body>
  <header>
    <div class="brand">
      <div class="logo-badge">PK</div>
      <div>
        <div class="brand-title">Portkey AI Gateway</div>
      </div>
      <span class="status-pill"><span class="status-dot"></span> Online (Port 8787)</span>
    </div>
    <div class="nav-links">
      <a href="/admin/routers" class="nav-btn">← CleverRoute Admin</a>
      <a href="https://docs.portkey.ai" target="_blank" rel="noreferrer" class="nav-btn primary">Portkey Docs ↗</a>
    </div>
  </header>

  <main>
    <div class="banner">
      <div class="banner-info">
        <h2>Router Instance: ` + slug + `</h2>
        <p>Ultra-fast multi-provider AI Gateway with fallback routing, load balancing, and prompt management.</p>
      </div>
      <div class="endpoint-box">
        <span>Base URL:</span>
        <span id="baseUrlDisplay">/` + slug + `/v1</span>
      </div>
    </div>

    <div class="tabs">
      <button class="tab-btn active" onclick="showTab('playground')">⚡ Live API Playground</button>
      <button class="tab-btn" onclick="showTab('config')">⚙️ Config & Strategy Builder</button>
      <button class="tab-btn" onclick="showTab('snippets')">💻 Client SDK Snippets</button>
    </div>

    <!-- TAB 1: PLAYGROUND -->
    <div id="tab-playground" class="tab-content active">
      <div class="grid-2">
        <div class="card">
          <div class="card-title"><span>📡 Request Parameters</span></div>
          <div class="form-group">
            <label>AI Provider (x-portkey-provider)</label>
            <select id="reqProvider" onchange="onProviderChange()">
              <option value="openai">OpenAI</option>
              <option value="anthropic">Anthropic</option>
              <option value="gemini">Google Gemini</option>
              <option value="deepseek">DeepSeek</option>
              <option value="groq">Groq</option>
              <option value="mistral">Mistral AI</option>
              <option value="cohere">Cohere</option>
            </select>
          </div>
          <div class="form-group">
            <label>Model ID</label>
            <input type="text" id="reqModel" value="gpt-4o" placeholder="e.g. gpt-4o">
          </div>
          <div class="form-group">
            <label>Provider API Key (Optional / x-portkey-api-key)</label>
            <input type="password" id="reqApiKey" placeholder="sk-..." oninput="saveApiKey(this.value)">
          </div>
          <div class="form-group">
            <label>Prompt / Chat Message</label>
            <textarea id="reqPrompt" rows="3">Explain quantum computing in one short sentence.</textarea>
          </div>
          <button class="btn" id="sendBtn" onclick="sendTestRequest()">
            <span>🚀 Send Request via Portkey</span>
          </button>
        </div>

        <div class="card">
          <div class="card-title" style="justify-content: space-between;">
            <span>📥 Gateway Response</span>
            <span id="metricBadge" class="metric-tag" style="display:none;"></span>
          </div>
          <div class="output-area" id="outputConsole">Ready. Click "Send Request via Portkey" to execute a live AI completion through this router instance.</div>
        </div>
      </div>
    </div>

    <!-- TAB 2: CONFIG BUILDER -->
    <div id="tab-config" class="tab-content">
      <div class="card">
        <div class="card-title"><span>⚙️ Multi-Provider Fallback & Load Balancer Generator</span></div>
        <p style="font-size:0.85rem; color:var(--text-muted); margin-bottom:1rem;">
          Generate <code>x-portkey-config</code> JSON payloads to enable zero-downtime provider fallbacks and traffic distribution.
        </p>
        <div class="grid-2">
          <div>
            <div class="form-group">
              <label>Strategy Mode</label>
              <select id="configMode" onchange="generateConfig()">
                <option value="fallback">Fallback (Primary -> Secondary on error)</option>
                <option value="loadbalance">Load Balance (Distribute across providers)</option>
              </select>
            </div>
            <div class="form-group">
              <label>Primary Provider</label>
              <select id="primaryProv" onchange="generateConfig()">
                <option value="openai">OpenAI (gpt-4o)</option>
                <option value="anthropic">Anthropic (claude-3-5-sonnet)</option>
                <option value="deepseek">DeepSeek (deepseek-chat)</option>
              </select>
            </div>
            <div class="form-group">
              <label>Fallback Provider</label>
              <select id="fallbackProv" onchange="generateConfig()">
                <option value="deepseek">DeepSeek (deepseek-chat)</option>
                <option value="anthropic">Anthropic (claude-3-5-sonnet)</option>
                <option value="openai">OpenAI (gpt-4o)</option>
              </select>
            </div>
            <div class="form-group">
              <label>Max Retries</label>
              <input type="number" id="configRetries" value="3" min="1" max="5" oninput="generateConfig()">
            </div>
          </div>
          <div>
            <label>Generated x-portkey-config Header JSON</label>
            <div class="code-block" id="configOutput">
              <button class="copy-btn" onclick="copyConfig()">Copy</button>
              <code id="configJsonCode"></code>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- TAB 3: CODE SNIPPETS -->
    <div id="tab-snippets" class="tab-content">
      <div class="card">
        <div class="card-title"><span>💻 Client Integration Examples</span></div>
        <p style="font-size:0.85rem; color:var(--text-muted); margin-bottom:1.5rem;">
          Connect your application to this Portkey AI Gateway instance using standard OpenAI client libraries or native cURL requests.
        </p>

        <label>cURL</label>
        <div class="code-block">
          <button class="copy-btn" onclick="copySnippet('curlCode')">Copy</button>
          <code id="curlCode">curl -X POST https://YOUR_DOMAIN/` + slug + `/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "x-portkey-provider: openai" \
  -H "Authorization: Bearer YOUR_OPENAI_KEY" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello via Portkey!"}]
  }'</code>
        </div>

        <label style="margin-top:1.5rem;">Python (OpenAI SDK)</label>
        <div class="code-block">
          <button class="copy-btn" onclick="copySnippet('pythonCode')">Copy</button>
          <code id="pythonCode">from openai import OpenAI

client = OpenAI(
    base_url="https://YOUR_DOMAIN/` + slug + `/v1",
    api_key="YOUR_OPENAI_KEY",
    default_headers={"x-portkey-provider": "openai"}
)

response = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Hello via Portkey Gateway!"}]
)
print(response.choices[0].message.content)</code>
        </div>
      </div>
    </div>
  </main>

  <script>
    const slug = "` + slug + `";
    const origin = window.location.origin;
    document.getElementById('baseUrlDisplay').innerText = origin + '/' + slug + '/v1';

    // Populate saved API key from localStorage
    const savedKey = localStorage.getItem('portkey_test_api_key');
    if (savedKey) {
      document.getElementById('reqApiKey').value = savedKey;
    }

    function saveApiKey(val) {
      localStorage.setItem('portkey_test_api_key', val);
    }

    function onProviderChange() {
      const p = document.getElementById('reqProvider').value;
      const m = document.getElementById('reqModel');
      if (p === 'openai') m.value = 'gpt-4o';
      else if (p === 'anthropic') m.value = 'claude-3-5-sonnet-20241022';
      else if (p === 'gemini') m.value = 'gemini-1.5-flash';
      else if (p === 'deepseek') m.value = 'deepseek-chat';
      else if (p === 'groq') m.value = 'llama-3.3-70b-versatile';
      else if (p === 'mistral') m.value = 'mistral-large-latest';
      else if (p === 'cohere') m.value = 'command-r-plus';
    }

    function showTab(tabName) {
      document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
      document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
      event.currentTarget.classList.add('active');
      document.getElementById('tab-' + tabName).classList.add('active');
      if (tabName === 'config') generateConfig();
    }

    async function sendTestRequest() {
      const provider = document.getElementById('reqProvider').value;
      const model = document.getElementById('reqModel').value;
      const apiKey = document.getElementById('reqApiKey').value;
      const prompt = document.getElementById('reqPrompt').value;
      const consoleBox = document.getElementById('outputConsole');
      const metricBadge = document.getElementById('metricBadge');
      const sendBtn = document.getElementById('sendBtn');

      sendBtn.disabled = true;
      sendBtn.innerHTML = '<span>⏳ Processing via Portkey Gateway...</span>';
      consoleBox.innerText = 'Connecting to /' + slug + '/v1/chat/completions...\nProvider: ' + provider + '\nModel: ' + model;

      const headers = {
        'Content-Type': 'application/json',
        'x-portkey-provider': provider
      };
      if (apiKey) {
        headers['Authorization'] = 'Bearer ' + apiKey;
        headers['x-portkey-api-key'] = apiKey;
      }

      const body = JSON.stringify({
        model: model,
        messages: [{ role: 'user', content: prompt }]
      });

      const startTime = performance.now();
      try {
        const resp = await fetch('/' + slug + '/v1/chat/completions', {
          method: 'POST',
          headers: headers,
          body: body
        });
        const elapsed = Math.round(performance.now() - startTime);
        const data = await resp.json();

        metricBadge.style.display = 'inline-block';
        if (resp.ok) {
          metricBadge.className = 'metric-tag metric-success';
          metricBadge.innerText = resp.status + ' OK · ' + elapsed + ' ms';
        } else {
          metricBadge.className = 'metric-tag metric-error';
          metricBadge.innerText = 'HTTP ' + resp.status + ' · ' + elapsed + ' ms';
        }

        consoleBox.innerText = JSON.stringify(data, null, 2);
      } catch (err) {
        const elapsed = Math.round(performance.now() - startTime);
        metricBadge.style.display = 'inline-block';
        metricBadge.className = 'metric-tag metric-error';
        metricBadge.innerText = 'Network Error · ' + elapsed + ' ms';
        consoleBox.innerText = 'Failed to fetch: ' + err.message;
      } finally {
        sendBtn.disabled = false;
        sendBtn.innerHTML = '<span>🚀 Send Request via Portkey</span>';
      }
    }

    function generateConfig() {
      const mode = document.getElementById('configMode').value;
      const primary = document.getElementById('primaryProv').value;
      const fallback = document.getElementById('fallbackProv').value;
      const retries = parseInt(document.getElementById('configRetries').value) || 3;

      let cfg;
      if (mode === 'fallback') {
        cfg = {
          strategy: {
            mode: 'fallback',
            on_status_codes: [429, 500, 502, 503, 504]
          },
          targets: [
            { provider: primary, override_params: { model: primary === 'openai' ? 'gpt-4o' : 'claude-3-5-sonnet' } },
            { provider: fallback, override_params: { model: fallback === 'deepseek' ? 'deepseek-chat' : 'gpt-4o' } }
          ],
          retry: { attempts: retries }
        };
      } else {
        cfg = {
          strategy: { mode: 'loadbalance' },
          targets: [
            { provider: primary, weight: 0.5 },
            { provider: fallback, weight: 0.5 }
          ]
        };
      }
      document.getElementById('configJsonCode').innerText = JSON.stringify(cfg, null, 2);
    }

    function copyConfig() {
      const text = document.getElementById('configJsonCode').innerText;
      navigator.clipboard.writeText(text);
      alert('Copied x-portkey-config JSON!');
    }

    function copySnippet(id) {
      const text = document.getElementById(id).innerText.replace(/https:\/\/YOUR_DOMAIN/g, origin);
      navigator.clipboard.writeText(text);
      alert('Code snippet copied to clipboard!');
    }
  </script>
</body>
</html>`)
}

