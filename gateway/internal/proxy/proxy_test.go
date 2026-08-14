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
