package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clever-route/gateway/internal/router"
	"github.com/gin-gonic/gin"
)

func TestIsWebOrAssetPath(t *testing.T) {
	cases := []struct {
		path     string
		expected bool
	}{
		{"/dashboard", true},
		{"/login", true},
		{"/setup", true},
		{"/settings", true},
		{"/_next/static/chunks/main.js", true},
		{"/static/css/app.css", true},
		{"/api/auth/login", true},
		{"/api/trpc/session", true},
		{"/favicon.ico", true},
		{"/v1/chat/completions", false},
		{"/v1/models", false},
	}

	for _, c := range cases {
		got := isWebOrAssetPath(c.path)
		if got != c.expected {
			t.Errorf("isWebOrAssetPath(%q) = %v; want %v", c.path, got, c.expected)
		}
	}
}

func TestCopyHeadersPreservesNativeToken(t *testing.T) {
	src := http.Header{}
	src.Set("Authorization", "Bearer omniroute-custom-jwt-token-12345")
	src.Set("Content-Type", "application/json")
	src.Set("X-Custom", "test")

	dst := http.Header{}
	copyHeaders(dst, src)

	if dst.Get("Authorization") != "Bearer omniroute-custom-jwt-token-12345" {
		t.Errorf("expected native authorization header preserved, got %q", dst.Get("Authorization"))
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("expected content-type preserved, got %q", dst.Get("Content-Type"))
	}
}

func TestCopyHeadersStripsVirtualKey(t *testing.T) {
	src := http.Header{}
	src.Set("Authorization", "Bearer cr-live-virtual-key-secret")
	src.Set("Content-Type", "application/json")

	dst := http.Header{}
	copyHeaders(dst, src)

	if dst.Get("Authorization") != "" {
		t.Errorf("expected virtual key authorization header stripped, got %q", dst.Get("Authorization"))
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("expected content-type preserved, got %q", dst.Get("Content-Type"))
	}
}

func TestProxyWebAssetPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Mock upstream router server
	upstreamCalled := false
	var receivedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"logged_in","user":"admin"}`))
	}))
	defer upstream.Close()

	table := router.NewTable(nil)
	table.Set("omniroute", upstream.URL)

	p := New(table, nil, nil, nil, nil)

	r := gin.New()
	r.Any("/:slug", p.Handle)
	r.Any("/:slug/*path", p.Handle)

	// Request to native login endpoint with fallback router cookie
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"CHANGEME"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "cr_active_router", Value: "omniroute"})
	req.Header.Set("Authorization", "Bearer omniroute-session-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !upstreamCalled {
		t.Fatalf("expected upstream to be called for /api/auth/login, but was not")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from upstream, got %d (body: %s)", w.Code, w.Body.String())
	}
	if receivedAuth != "Bearer omniroute-session-token" {
		t.Errorf("expected upstream to receive native token, got %q", receivedAuth)
	}
}

func TestProxyNativeApiKeyPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamCalled := false
	var receivedAuth string
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		receivedAuth = r.Header.Get("Authorization")
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o","object":"model"}]}`))
	}))
	defer upstream.Close()

	table := router.NewTable(nil)
	table.Set("omnirouter", upstream.URL)

	p := New(table, nil, nil, nil, nil)

	r := gin.New()
	r.Any("/:slug", p.Handle)
	r.Any("/:slug/*path", p.Handle)

	req := httptest.NewRequest(http.MethodGet, "/omnirouter/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-e3233a008021f896-f33a2c-f8a42a41")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !upstreamCalled {
		t.Fatalf("expected upstream to be called for /omnirouter/v1/models")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
	}
	if receivedAuth != "Bearer sk-e3233a008021f896-f33a2c-f8a42a41" {
		t.Errorf("expected upstream to receive native sk key, got %q", receivedAuth)
	}
	if receivedPath != "/v1/models" {
		t.Errorf("expected upstream path /v1/models, got %q", receivedPath)
	}
}

func TestProxyAutomaticPathMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Upstream router serves models only on /api/v1/models, returns 404 on /v1/models
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gemini-pro","object":"model"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	table := router.NewTable(nil)
	table.Set("omnirouter", upstream.URL)

	p := New(table, nil, nil, nil, nil)

	r := gin.New()
	r.Any("/:slug", p.Handle)
	r.Any("/:slug/*path", p.Handle)

	// Client calls /omnirouter/v1/models
	req := httptest.NewRequest(http.MethodGet, "/omnirouter/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-custom-key")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected automatic path mapping to succeed with 200 OK, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "gemini-pro") {
		t.Fatalf("expected response body to contain model data, got: %s", w.Body.String())
	}
}

