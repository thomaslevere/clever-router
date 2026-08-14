package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clever-route/gateway/internal/config"
	"github.com/clever-route/gateway/internal/store"
	"github.com/gin-gonic/gin"
)

func newTestAPI(t *testing.T) *API {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return New(Deps{Cfg: &config.Config{AdminToken: "secret", AdminInternalAddr: "127.0.0.1:1"}})
}

func TestRegisterNoPanic(t *testing.T) {
	a := newTestAPI(t)
	r := gin.New()
	r.Use(gin.Recovery())
	a.Register(r)
	if len(r.Routes()) == 0 {
		t.Fatal("expected routes to be registered")
	}
}

func TestRestDispatchAuthRequired(t *testing.T) {
	a := newTestAPI(t)
	r := gin.New()
	r.Use(gin.Recovery())
	a.Register(r)

	// No Authorization header -> 401 from the REST engine's auth middleware.
	req := httptest.NewRequest(http.MethodGet, "/admin/api/routers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated REST call, got %d", w.Code)
	}

	// Valid token reaches the handler (store is nil -> recovered 500, not 401/404).
	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/routers", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code == http.StatusUnauthorized {
		t.Fatalf("valid token should not be rejected, got 401")
	}
}

func TestHealthzOK(t *testing.T) {
	a := newTestAPI(t)
	r := gin.New()
	r.Use(gin.Recovery())
	a.Register(r)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// store/cache are nil -> Ping panics -> recovered -> 500. Ensure not a crash
	// and that the route itself is wired (status 500, not 404).
	if w.Code == http.StatusNotFound {
		t.Fatalf("/healthz should be registered, got 404")
	}
}

func TestSingleJSONResponse(t *testing.T) {
	a := newTestAPI(t)
	r := gin.New()
	r.Use(gin.Recovery())
	a.Register(r)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/routers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Verify the body is valid single JSON, not duplicated JSON
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	expected := `{"error":"unauthorized"}`
	if w.Body.String() != expected {
		t.Fatalf("expected exact single body %q, got %q", expected, w.Body.String())
	}
}

func TestEnvKeyValidation(t *testing.T) {
	validKeys := []string{"JWT_SECRET", "API_KEY_SECRET", "INITIAL_PASSWORD", "PORT", "DATA_DIR", "_CUSTOM_VAR", "VAR_123"}
	for _, k := range validKeys {
		if !envKeyRe.MatchString(k) {
			t.Errorf("expected key %q to be valid", k)
		}
	}

	invalidKeys := []string{"123VAR", "KEY-WITH-HYPHEN", "KEY.WITH.DOT", "KEY WITH SPACE", "KEY$VAR"}
	for _, k := range invalidKeys {
		if envKeyRe.MatchString(k) {
			t.Errorf("expected key %q to be invalid", k)
		}
	}
}

func TestMaskRouterSecrets(t *testing.T) {
	r := &store.Router{
		ID:   "test-id",
		Slug: "omniroute",
		EnvVars: []store.EnvVariable{
			{Key: "PORT", Value: "20128", IsSecret: false},
			{Key: "JWT_SECRET", Value: "enc:abcdef123456", IsSecret: true},
			{Key: "INITIAL_PASSWORD", Value: "AdminPass123!", IsSecret: true},
		},
	}

	masked := maskRouterSecrets(r)
	if len(masked.EnvVars) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(masked.EnvVars))
	}
	if masked.EnvVars[0].Value != "20128" {
		t.Errorf("expected non-secret value unchanged, got %q", masked.EnvVars[0].Value)
	}
	if masked.EnvVars[1].Value != "********" {
		t.Errorf("expected secret value to be masked, got %q", masked.EnvVars[1].Value)
	}
	if masked.EnvVars[2].Value != "********" {
		t.Errorf("expected secret value to be masked, got %q", masked.EnvVars[2].Value)
	}
}

func TestAPIRouting(t *testing.T) {
	a := newTestAPI(t)
	r := gin.New()
	r.Use(gin.Recovery())
	a.Register(r)

	// Test /api/routers endpoint reaches the router
	req := httptest.NewRequest(http.MethodGet, "/api/routers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/routers call, got %d", w.Code)
	}
}


