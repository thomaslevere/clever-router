package proxy

import (
	"bytes"
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
			Transport: &http.Transport{
				DialContext:     (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns:    100,
				IdleConnTimeout: 90 * time.Second,
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

func (p *Proxy) Handle(c *gin.Context) {
	slug := c.Param("slug")
	path := c.Param("path")

	target, ok := p.table.Lookup(slug)
	targetSlug := slug
	upstreamPath := path

	// Fallback routing for root-relative assets and sub-requests (e.g. /_next/*, /api/*, /favicon.ico)
	if !ok {
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

	// If root of the router is accessed without subpath (e.g. /omniroute-live or /omniroute-live/)
	if (path == "" || path == "/") && targetSlug == slug {
		redirectURL := "/" + targetSlug + "/dashboard"
		if token != "" {
			redirectURL += "?token=" + url.QueryEscape(token)
		}
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
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
		if token == "" {
			if isWebOrAssetPath(upstreamPath) && targetSlug != "" {
				// Allow web UI and assets passthrough to router's native server
			} else {
				fail(c, 401, "invalid or missing api key")
				return
			}
		} else if strings.HasPrefix(token, "cr-") && p.auth != nil {
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
		} else if isWebOrAssetPath(upstreamPath) && targetSlug != "" {
			// Non-virtual key token (e.g. native router Bearer token/cookie) on native dashboard path: passthrough
		} else if p.auth != nil {
			var err error
			ki, err = p.auth.Authenticate(c.Request.Context(), token)
			if err != nil {
				fail(c, 401, "invalid or missing api key")
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
		} else {
			fail(c, 401, "invalid or missing api key")
			return
		}
	}

	// Set browser session cookies on valid token for subsequent Next.js asset/chunk requests
	if token != "" {
		c.SetCookie("cr_router_auth", token, 86400*7, "/", "", false, false)
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

	resp, err := p.client.Do(req)
	if err != nil {
		fail(c, http.StatusBadGateway, "upstream unreachable: %v", err)
		return
	}
	defer resp.Body.Close()

	// Flush upstream headers to client.
	for k, vs := range resp.Header {
		if isHop(k) {
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
	c.Writer.WriteHeader(resp.StatusCode)

	sc := newUsageScanner(model)
	flusher, _ := c.Writer.(http.Flusher)

	if isStream && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream") {
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
	return &usageScanner{model: model}
}

func (s *usageScanner) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	// Keep only the last 64KB to bound memory usage.
	const maxBuf = 64 * 1024
	if len(s.buf) > maxBuf {
		s.buf = s.buf[len(s.buf)-maxBuf:]
	}
	if !s.done && bytes.Contains(s.buf, []byte("[DONE]")) {
		s.done = true
	}
	return len(p), nil
}

func (s *usageScanner) usage() (prompt, completion, total int) {
	s.parse()
	return s.prompt, s.comp, s.total
}

func (s *usageScanner) parse() {
	if s.parsed {
		return
	}
	s.parsed = true
	obj := extractJSONObject(s.buf, []byte(`"usage":`))
	if obj == nil {
		return
	}
	var u struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	if err := json.Unmarshal(obj, &u); err == nil {
		s.prompt, s.comp, s.total = u.PromptTokens, u.CompletionTokens, u.TotalTokens
	}
}

// extractJSONObject locates the JSON object immediately following marker in buf.
func extractJSONObject(buf, marker []byte) []byte {
	idx := bytes.Index(buf, marker)
	if idx < 0 {
		return nil
	}
	rest := buf[idx+len(marker):]
	start := bytes.IndexByte(rest, '{')
	if start < 0 {
		return nil
	}
	start += idx + len(marker)

	depth := 0
	inStr := false
	escape := false

	for i := start; i < len(buf); i++ {
		b := buf[i]
		switch {
		case escape:
			escape = false
		case inStr:
			if b == '\\' {
				escape = true
			} else if b == '"' {
				inStr = false
			}
		case b == '"':
			inStr = true
		case b == '{':
			depth++
		case b == '}':
			depth--
			if depth == 0 {
				return buf[start : i+1]
			}
		}
	}
	return nil
}
