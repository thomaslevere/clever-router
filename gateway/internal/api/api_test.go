package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clever-route/gateway/internal/config"
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

