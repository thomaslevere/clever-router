package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/clever-route/gateway/internal/keys"
	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/store"
	"github.com/gin-gonic/gin"
)

// Proxy is the namespaced AI request path. It authenticates a virtual key,
// resolves the router target from the hot routing table, forwards the request
// to the sibling container, and streams the response back while a lightweight
// byte scanner captures token usage for billing/quota — without full JSON
// deserialization of every chunk.
type Proxy struct {
	table  *router.Table
	auth   *keys.Auth
	store  *store.Store
	client *http.Client
}

func New(t *router.Table, a *keys.Auth, st *store.Store) *Proxy {
	return &Proxy{
		table: t, auth: a, store: st,
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
	target, ok := p.table.Lookup(slug)
	if !ok {
		fail(c, http.StatusBadGateway, "router '%s' is not serving", slug)
		return
	}

	ki, err := p.auth.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
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

	// M-1 FIX: AllowRouter was called twice. Single authoritative check here.
	if !keys.AllowRouter(ki.RouterAllow, slug) {
		fail(c, 403, "key not allowed for router '%s'", slug)
		return
	}
	if err := p.auth.CheckRate(c.Request.Context(), ki.ID, ki.RateLimitRPM); err != nil {
		fail(c, 429, "rate limit exceeded")
		return
	}

	// BUG-2 FIX: enforce budget before forwarding the request.
	// BudgetCents == 0 means unlimited.
	if ki.BudgetCents > 0 {
		ok, err := p.store.CheckBudget(c.Request.Context(), ki.ID)
		if err == nil && !ok {
			fail(c, 403, "budget exhausted for this key")
			return
		}
	}

	body, _ := io.ReadAll(c.Request.Body)
	model, isStream := extractModel(body)
	if model != "" && !keys.AllowModel(ki.ModelAllow, model) {
		fail(c, 403, "model '%s' not allowed for this key", model)
		return
	}

	upURL := strings.TrimRight(target, "/") + c.Param("path")
	if c.Request.URL.RawQuery != "" {
		upURL += "?" + c.Request.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upURL, bytes.NewReader(body))
	if err != nil {
		fail(c, 500, "build upstream request: %v", err)
		return
	}
	copyHeaders(req.Header, c.Request.Header, true)
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
	c.Writer.Header().Set("X-CleverRoute-Router", slug)
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
				c.Writer.Write(buf[:n])
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
		c.Writer.Write(bufBody)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Record usage asynchronously so the client is never blocked on the DB.
	go p.recordUsage(ki.ID, slug, model, resp.StatusCode, sc)
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

func copyHeaders(dst, src http.Header, skipAuth bool) {
	for k, vs := range src {
		if isHop(k) {
			continue
		}
		if skipAuth && strings.EqualFold(k, "Authorization") {
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
//
// BUG-3 FIX: The previous implementation toggled inStr on every '"' byte,
// which broke on escaped quotes (e.g. `\"`) inside string values — causing
// the brace-depth counter to miscount and return a truncated or nil object.
// This version correctly tracks escape state: a '"' preceded by an odd number
// of backslashes does not change the string boundary.
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
			// Previous character was a backslash — this character is always
			// a literal (escaped), never a string boundary or brace.
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
