package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/clever-route/gateway/internal/adapters"
	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/config"
	"github.com/clever-route/gateway/internal/keys"
	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/proxy"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/storage"
	"github.com/clever-route/gateway/internal/store"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// slugRe validates that a router slug is safe for use as a container name,
// Docker volume name, and URL path segment.
// GAP-3 FIX: slugs are used in container names and URL routing — an invalid
// slug could cause container name injection or routing conflicts.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,61}[a-z0-9]$`)

// envKeyRe validates that environment variable keys follow standard identifier format.
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedSlugs are path prefixes that must never be registered as routers.
var reservedSlugs = map[string]bool{
	"admin":   true,
	"api":     true,
	"healthz": true,
	"metrics": true,
}

func validateSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("slug must be 2–63 lowercase alphanumeric characters or hyphens (e.g. omniroute-prod)")
	}
	if reservedSlugs[slug] {
		return fmt.Errorf("slug %q is reserved", slug)
	}
	return nil
}

type API struct {
	cfg      *config.Config
	store    *store.Store
	cache    *cache.Cache
	box      *secrets.Box
	manager  *adapters.Manager
	table    *router.Table
	rest     *gin.Engine // private engine mounted under /admin/api/* and /api/*
	logHub   *WSHub
	auditHub *WSHub
	bridge   *storage.FastVolumeBridge
	explorer *storage.StorageExplorer
}

type Deps struct {
	Cfg     *config.Config
	Store   *store.Store
	Cache   *cache.Cache
	Box     *secrets.Box
	Manager *adapters.Manager
	Table   *router.Table
	Bridge  *storage.FastVolumeBridge
}

func New(d Deps) *API {
	logHub := newWSHub(d.Cache)
	auditHub := newWSHub(d.Cache)
	logHub.StartListening(context.Background(), "events:logs")
	auditHub.StartListening(context.Background(), "events:audit")

	scratchDir := "/tmp/clever_router_volumes"
	if d.Cfg != nil && d.Cfg.VolumeScratchDir != "" {
		scratchDir = d.Cfg.VolumeScratchDir
	}
	dataDir := "/tmp/data"
	if d.Cfg != nil && d.Cfg.PostgresURL != "" {
		dataDir = "logs"
	}

	explorer := storage.NewStorageExplorer(d.Bridge, []string{
		scratchDir,
		"/tmp/clever_router_volumes",
		"/tmp/data",
		dataDir,
	})

	return &API{
		cfg:      d.Cfg,
		store:    d.Store,
		cache:    d.Cache,
		box:      d.Box,
		manager:  d.Manager,
		table:    d.Table,
		logHub:   logHub,
		auditHub: auditHub,
		bridge:   d.Bridge,
		explorer: explorer,
	}
}

// Register mounts every route on the engine: health, the admin UI
// reverse-proxy, the admin REST API (dispatched through a private engine to
// avoid gin catch-all/static conflicts), and the namespaced AI proxy.
func (a *API) Register(r *gin.Engine) {
	r.GET("/healthz", a.healthz)

	// Admin UI (Next.js) reverse-proxied to the in-container :3000. /admin/api/*
	// requests are dispatched to the REST engine below before reaching the UI.
	r.Any("/admin", a.adminRoot)
	r.Any("/admin/*any", a.adminRoot)

	// Direct REST API (/api/*)
	r.Any("/api", a.apiRoot)
	r.Any("/api/*any", a.apiRoot)

	// Private REST engine. Routes are registered at its root and dispatched by
	// adminRoot after stripping the /admin/api prefix — this keeps the public
	// /admin/*any catch-all conflict-free with gin's radix tree.
	rest := gin.New()
	rest.Use(gin.Recovery(), a.adminAuth())
	a.registerAdmin(rest.Group("/"))
	a.rest = rest

	// Namespaced AI proxy: /{slug}/v1/... and /{slug}/{native}/...
	p := proxy.New(a.table, keys.NewAuth(a.store, a.cache), a.store, a.cache, a.cfg)
	r.Any("/:slug", p.Handle)
	r.Any("/:slug/*path", p.Handle)
}

// adminRoot routes /admin/api/* to the private REST engine and everything else
// under /admin to the Next.js UI reverse proxy.
func (a *API) adminRoot(c *gin.Context) {
	p := c.Request.URL.Path
	if p == "/admin/api" || strings.HasPrefix(p, "/admin/api/") {
		rest := strings.TrimPrefix(p, "/admin/api")
		if rest == "" {
			rest = "/"
		}
		origPath := c.Request.URL.Path
		origRawPath := c.Request.URL.RawPath
		c.Request.URL.Path = rest
		c.Request.URL.RawPath = ""
		a.rest.ServeHTTP(c.Writer, c.Request)
		c.Request.URL.Path = origPath
		c.Request.URL.RawPath = origRawPath
		c.Abort()
		return
	}
	a.adminUI(c)
}

// apiRoot routes /api/* directly to the private REST engine.
func (a *API) apiRoot(c *gin.Context) {
	p := c.Request.URL.Path
	rest := strings.TrimPrefix(p, "/api")
	if rest == "" {
		rest = "/"
	}
	origPath := c.Request.URL.Path
	origRawPath := c.Request.URL.RawPath
	c.Request.URL.Path = rest
	c.Request.URL.RawPath = ""
	a.rest.ServeHTTP(c.Writer, c.Request)
	c.Request.URL.Path = origPath
	c.Request.URL.RawPath = origRawPath
	c.Abort()
}

func (a *API) registerAdmin(g *gin.RouterGroup) {
	// Authentication
	g.POST("/auth/login", a.login)
	g.POST("/auth/logout", a.logout)
	g.GET("/auth/me", a.authMe)

	// Real-time WebSockets
	g.GET("/ws/logs", a.wsLogs)
	g.GET("/ws/audit", a.wsAudit)
	g.GET("/ws/terminal", a.wsTerminal)

	// System & Aggregator Logs
	g.GET("/logs", a.listLogs)
	g.GET("/logs/download", a.downloadLogs)
	g.GET("/audit/download", a.downloadAudit)

	// Router CRUD
	g.GET("/routers", a.listRouters)
	g.POST("/routers", a.createRouter)
	g.GET("/routers/:id", a.getRouter)
	g.PATCH("/routers/:id", a.updateRouter)
	g.DELETE("/routers/:id", a.deleteRouter)

	// Router environment variables
	g.GET("/routers/:id/env", a.getRouterEnv)
	g.PUT("/routers/:id/env", a.putRouterEnv)
	g.POST("/routers/:id/env/apply", a.applyRouterEnv)

	// Router lifecycle
	g.POST("/routers/:id/start", a.startRouter)
	g.POST("/routers/:id/stop", a.stopRouter)
	g.POST("/routers/:id/restart", a.restartRouter)
	g.POST("/routers/:id/discover", a.discoverRouter)
	g.GET("/routers/:id/health", a.healthRouter)
	g.GET("/routers/:id/models", a.listModels)
	g.GET("/routers/:id/logs", a.logsRouter)

	// Credentials — GAP-5 FIX: scoped under /routers/:id/credentials/:provider
	// for consistent REST semantics and traceable audit entries.
	g.GET("/routers/:id/credentials", a.listCredentials)
	g.PUT("/routers/:id/credentials/:provider", a.putCredential)
	g.DELETE("/routers/:id/credentials/:provider", a.deleteCredential)

	// Virtual keys
	g.GET("/keys", a.listKeys)
	g.POST("/keys", a.createKey)
	g.POST("/keys/:id/revoke", a.revokeKey)
	g.DELETE("/keys/:id", a.deleteKey)

	// Storage, Metrics & Cellar S3 Explorer
	g.GET("/storage/metrics", a.getStorageMetrics)
	g.GET("/storage/local/tree", a.listLocalFiles)
	g.GET("/storage/local/file", a.getLocalFile)
	g.GET("/storage/local/download", a.downloadLocalFile)
	g.DELETE("/storage/local/file", a.deleteLocalFile)
	g.GET("/storage/s3/objects", a.listS3Objects)
	g.GET("/storage/s3/download", a.downloadS3Object)
	g.POST("/storage/s3/sync", a.manualS3Sync)
	g.POST("/storage/s3/restore", a.manualS3Restore)
	g.DELETE("/storage/s3/object", a.deleteS3Object)

	// Audit + system
	g.GET("/audit", a.listAudit)
	g.GET("/system", a.systemInfo)
}

// ----- middleware -----

func (a *API) adminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Public login endpoint does not require pre-existing authentication.
		if c.Request.URL.Path == "/auth/login" {
			c.Next()
			return
		}
		if a.cfg.IsDev() && c.GetHeader("X-Dev-Bypass") == "1" {
			c.Set("actor", "dev")
			c.Set("role", "owner")
			c.Next()
			return
		}
		tok := ""
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tok = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
		if tok == "" {
			tok = strings.TrimSpace(c.Query("token"))
		}
		if tok == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}

		// 1. Direct master token check (CLI/system calls)
		if tok == a.cfg.AdminToken {
			c.Set("actor", "admin")
			c.Set("role", "owner")
			c.Next()
			return
		}

		// 2. User session check via Redis
		sess, err := a.cache.GetSession(c.Request.Context(), tok)
		if err == nil && sess != nil {
			c.Set("actor", sess.Username)
			c.Set("user_id", sess.UserID)
			c.Set("role", sess.Role)
			// Sliding 7-day expiration: extend TTL on active panel usage
			_ = a.cache.SetSession(c.Request.Context(), tok, sess, 7*24*time.Hour)
			c.Next()
			return
		}

		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
	}
}

// ----- health -----

func (a *API) healthz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	pgOK := a.store.Ping(ctx) == nil
	rdOK := a.cache.Ping(ctx) == nil
	if !pgOK || !rdOK {
		c.JSON(503, gin.H{"status": "unhealthy", "postgres": pgOK, "redis": rdOK})
		return
	}
	c.JSON(200, gin.H{"status": "healthy", "postgres": pgOK, "redis": rdOK, "routers": len(a.table.Snapshot())})
}

// ----- admin UI reverse proxy -----

func (a *API) adminUI(c *gin.Context) {
	target, err := url.Parse("http://" + a.cfg.AdminInternalAddr)
	if err != nil {
		c.String(500, "bad admin addr")
		return
	}
	p := httputil.NewSingleHostReverseProxy(target)
	p.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
	}
	p.ServeHTTP(c.Writer, c.Request)
}

// ----- routers -----

func (a *API) findRouter(ctx context.Context, idOrSlug string) (*store.Router, error) {
	r, err := a.store.GetRouter(ctx, idOrSlug)
	if err == nil && r != nil {
		return r, nil
	}
	return a.store.GetRouterBySlug(ctx, idOrSlug)
}

func maskRouterSecrets(r *store.Router) store.Router {
	if r == nil {
		return store.Router{}
	}
	out := *r
	if len(r.EnvVars) > 0 {
		masked := make([]store.EnvVariable, len(r.EnvVars))
		for i, ev := range r.EnvVars {
			masked[i] = ev
			if ev.IsSecret {
				masked[i].Value = "********"
			}
		}
		out.EnvVars = masked
	}
	return out
}

type createRouterReq struct {
	Slug                   string              `json:"slug" binding:"required"`
	Name                   string              `json:"name" binding:"required"`
	AdapterType            string              `json:"adapter_type" binding:"required"`
	ImageRef               string              `json:"image_ref" binding:"required"`
	DesiredVersion         string              `json:"desired_version"`
	EndpointPath           string              `json:"endpoint_path"`
	DesiredState           string              `json:"desired_state"`
	Config                 store.Map           `json:"config"`
	EnvVars                []store.EnvVariable `json:"env_vars"`
	AutoRestartOnEnvChange bool                `json:"auto_restart_on_env_change"`
}

func (a *API) listRouters(c *gin.Context) {
	rs, err := a.store.ListRouters(c)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	out := make([]store.Router, len(rs))
	for i, r := range rs {
		out[i] = maskRouterSecrets(&r)
	}
	c.JSON(200, out)
}

func (a *API) createRouter(c *gin.Context) {
	var req createRouterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// GAP-3 FIX: validate slug before writing to DB or passing to Docker.
	if err := validateSlug(req.Slug); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.EndpointPath == "" {
		req.EndpointPath = "/" + req.Slug
	}
	if req.DesiredVersion == "" {
		req.DesiredVersion = "latest"
	}
	if req.DesiredState == "" {
		req.DesiredState = "stopped"
	}
	if req.Config == nil {
		req.Config = store.Map{}
	}

	// Clean, validate, and encrypt initial env vars
	cleanedEnv := make([]store.EnvVariable, 0, len(req.EnvVars))
	for _, item := range req.EnvVars {
		k := strings.TrimSpace(item.Key)
		if k == "" {
			continue
		}
		if !envKeyRe.MatchString(k) {
			c.JSON(400, gin.H{"error": fmt.Sprintf("invalid environment variable key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$", k)})
			return
		}
		val := item.Value
		if item.IsSecret && !secrets.IsEncrypted(val) {
			enc, err := secrets.EncryptValue(a.box, val)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("failed to encrypt %s: %v", k, err)})
				return
			}
			val = enc
		}
		cleanedEnv = append(cleanedEnv, store.EnvVariable{
			Key:      k,
			Value:    val,
			IsSecret: item.IsSecret,
		})
	}

	r := &store.Router{
		Slug: req.Slug, Name: req.Name, AdapterType: req.AdapterType, ImageRef: req.ImageRef,
		DesiredVersion: req.DesiredVersion, EndpointPath: req.EndpointPath,
		DesiredState: req.DesiredState, Config: req.Config,
		EnvVars: cleanedEnv, AutoRestartOnEnvChange: req.AutoRestartOnEnvChange,
	}
	if err := a.store.CreateRouter(c, r); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "router.create", "router", r.ID, nil, store.Map{"slug": r.Slug, "adapter": r.AdapterType, "image": r.ImageRef})
	masked := maskRouterSecrets(r)
	c.JSON(201, masked)
}

func (a *API) getRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	masked := maskRouterSecrets(r)
	c.JSON(200, masked)
}

func (a *API) updateRouter(c *gin.Context) {
	var req struct {
		Name   string    `json:"name"`
		Config store.Map `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	before, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if err := a.store.UpdateRouter(c, before.ID, req.Name, req.Config); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "router.update", "router", before.ID, before, store.Map{"name": req.Name, "config": req.Config})
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) deleteRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if r.DesiredState == "running" || r.RuntimeState == "running" {
		_ = a.manager.Stop(c, r)
	}
	if err := a.store.DeleteRouter(c, r.ID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "router.delete", "router", r.ID, r, nil)
	c.JSON(200, gin.H{"ok": true})
}

// ----- environment variables -----

func (a *API) getRouterEnv(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	masked := maskRouterSecrets(r)
	c.JSON(200, gin.H{
		"env_vars":                  masked.EnvVars,
		"auto_restart_on_env_change": r.AutoRestartOnEnvChange,
	})
}

type putRouterEnvReq struct {
	EnvVars                []store.EnvVariable `json:"env_vars"`
	AutoRestartOnEnvChange *bool               `json:"auto_restart_on_env_change"`
	RestartNow             bool                `json:"restart_now"`
}

func (a *API) putRouterEnv(c *gin.Context) {
	var req putRouterEnvReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	target := c.Param("id")
	existing, err := a.findRouter(c, target)
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}

	// Index existing secrets by key to preserve unchanged masked values
	existingSecrets := make(map[string]store.EnvVariable, len(existing.EnvVars))
	for _, ev := range existing.EnvVars {
		existingSecrets[ev.Key] = ev
	}

	cleanedEnv := make([]store.EnvVariable, 0, len(req.EnvVars))
	for _, item := range req.EnvVars {
		k := strings.TrimSpace(item.Key)
		if k == "" {
			continue
		}
		if !envKeyRe.MatchString(k) {
			c.JSON(400, gin.H{"error": fmt.Sprintf("invalid environment variable key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$", k)})
			return
		}

		val := item.Value
		if item.IsSecret {
			// Check if value is masked placeholder ("********" or "****") or empty to keep existing secret
			if val == "********" || val == "****" || val == "" {
				if ex, ok := existingSecrets[k]; ok {
					val = ex.Value // keep existing encrypted/plaintext value
				} else {
					enc, err := secrets.EncryptValue(a.box, "")
					if err != nil {
						c.JSON(500, gin.H{"error": "encryption error"})
						return
					}
					val = enc
				}
			} else if !secrets.IsEncrypted(val) {
				enc, err := secrets.EncryptValue(a.box, val)
				if err != nil {
					c.JSON(500, gin.H{"error": fmt.Sprintf("failed to encrypt %s: %v", k, err)})
					return
				}
				val = enc
			}
		} else {
			// Non-secret: decrypt if it was previously encrypted
			if secrets.IsEncrypted(val) {
				dec, err := secrets.DecryptValue(a.box, val)
				if err == nil {
					val = dec
				}
			}
		}

		cleanedEnv = append(cleanedEnv, store.EnvVariable{
			Key:      k,
			Value:    val,
			IsSecret: item.IsSecret,
		})
	}

	autoRestart := existing.AutoRestartOnEnvChange
	if req.AutoRestartOnEnvChange != nil {
		autoRestart = *req.AutoRestartOnEnvChange
	}

	if err := a.store.UpdateRouterEnv(c, existing.ID, cleanedEnv, autoRestart); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	a.audit(c, "router.env.update", "router", existing.ID, nil, store.Map{
		"env_vars_count": len(cleanedEnv),
		"auto_restart":   autoRestart,
		"restart_now":    req.RestartNow,
	})

	shouldRestart := req.RestartNow || (autoRestart && (existing.RuntimeState == "running" || existing.DesiredState == "running"))
	if shouldRestart {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if rUpdated, err := a.store.GetRouter(ctx, existing.ID); err == nil {
				if err := a.manager.Restart(ctx, rUpdated); err != nil {
					log.Printf("[api] restart after env update %s: %v", rUpdated.Slug, err)
				}
			}
		}()
	}

	c.JSON(200, gin.H{
		"ok":                true,
		"restart_triggered": shouldRestart,
	})
}

func (a *API) applyRouterEnv(c *gin.Context) {
	target := c.Param("id")
	r, err := a.findRouter(c, target)
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if rUpdated, err := a.store.GetRouter(ctx, r.ID); err == nil {
			if err := a.manager.Restart(ctx, rUpdated); err != nil {
				log.Printf("[api] apply env restart %s: %v", rUpdated.Slug, err)
			}
		}
	}()

	a.audit(c, "router.env.apply", "router", r.ID, nil, nil)
	c.JSON(202, gin.H{"ok": true, "state": "restarting"})
}

func (a *API) startRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	_ = a.store.SetDesiredState(c, r.ID, "running")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.manager.Start(ctx, r); err != nil {
			log.Printf("[api] start %s: %v", r.Slug, err)
		}
	}()
	a.audit(c, "router.start", "router", r.ID, nil, nil)
	c.JSON(202, gin.H{"ok": true, "state": "starting"})
}

func (a *API) stopRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	_ = a.store.SetDesiredState(c, r.ID, "stopped")
	go func() {
		if err := a.manager.Stop(context.Background(), r); err != nil {
			log.Printf("[api] stop %s: %v", r.Slug, err)
		}
	}()
	a.audit(c, "router.stop", "router", r.ID, nil, nil)
	c.JSON(202, gin.H{"ok": true, "state": "stopping"})
}

func (a *API) restartRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := a.manager.Restart(ctx, r); err != nil {
			log.Printf("[api] restart %s: %v", r.Slug, err)
		}
	}()
	a.audit(c, "router.restart", "router", r.ID, nil, nil)
	c.JSON(202, gin.H{"ok": true, "state": "restarting"})
}

func (a *API) discoverRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	go func() {
		if err := a.manager.DiscoverModels(context.Background(), r, nil, ""); err != nil {
			log.Printf("[api] discover %s: %v", r.Slug, err)
		}
	}()
	c.JSON(202, gin.H{"ok": true})
}

func (a *API) healthRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	err = a.manager.HealthCheck(c, r)
	if err != nil {
		c.JSON(200, gin.H{"status": "unhealthy", "detail": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "healthy"})
}

func (a *API) listModels(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	ms, err := a.store.ListModels(c, r.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, ms)
}

// logsRouter streams container logs to the client.
// L-7 FIX: Docker container logs use a multiplexed format (8-byte header per
// frame). The previous implementation forwarded raw bytes, producing garbage
// binary prefixes in the browser. stdcopy.StdCopy demultiplexes stdout/stderr
// and writes clean UTF-8 lines to the pipe consumed by the SSE stream.
func (a *API) logsRouter(c *gin.Context) {
	r, err := a.findRouter(c, c.Param("id"))
	if err != nil || r.ContainerID == "" {
		c.JSON(404, gin.H{"error": "router not running"})
		return
	}
	rc, err := a.manager.Logs(c, r, true)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()

	// Demultiplex Docker's multiplexed log stream into a clean pipe.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, _ = stdcopy.StdCopy(pw, pw, rc)
	}()

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Stream(func(w io.Writer) bool {
		buf := make([]byte, 4096)
		n, err := pr.Read(buf)
		if err != nil {
			return false
		}
		_, _ = w.Write(buf[:n])
		return true
	})
}

// ----- credentials -----

type putCredentialReq struct {
	Key      string    `json:"key" binding:"required"`
	Metadata store.Map `json:"metadata"`
}

func (a *API) listCredentials(c *gin.Context) {
	creds, err := a.store.ListCredentials(c, c.Param("id"))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, len(creds))
	for i, cr := range creds {
		out[i] = gin.H{"id": cr.ID, "provider": cr.Provider, "metadata": cr.Metadata,
			"last_verified_at": cr.LastVerifiedAt, "created_at": cr.CreatedAt}
	}
	c.JSON(200, out)
}

func (a *API) putCredential(c *gin.Context) {
	var req putCredentialReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	ct, err := a.box.Encrypt([]byte(req.Key))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if req.Metadata == nil {
		req.Metadata = store.Map{}
	}
	cred := &store.ProviderCredential{
		RouterID: c.Param("id"), Provider: c.Param("provider"), Ciphertext: ct,
		KeyID: a.box.KeyID(), Metadata: req.Metadata,
	}
	if err := a.store.SaveCredential(c, cred); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "credential.save", "router", c.Param("id"), nil, store.Map{"provider": c.Param("provider")})
	c.JSON(201, gin.H{"ok": true, "provider": c.Param("provider")})
}

// deleteCredential removes a credential by router ID + provider name.
// GAP-5 FIX: Route is now DELETE /routers/:id/credentials/:provider, which is
// semantically correct (the resource is identified by router+provider pair).
func (a *API) deleteCredential(c *gin.Context) {
	if err := a.store.DeleteCredentialByProvider(c, c.Param("id"), c.Param("provider")); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "credential.delete", "router", c.Param("id"), nil, store.Map{"provider": c.Param("provider")})
	c.JSON(200, gin.H{"ok": true})
}

// ----- virtual keys -----

type createKeyReq struct {
	Name         string   `json:"name" binding:"required"`
	BudgetCents  int64    `json:"budget_cents"`
	RateLimitRPM int      `json:"rate_limit_rpm"`
	ModelAllow   []string `json:"model_allowlist"`
	RouterAllow  []string `json:"router_allowlist"`
}

func (a *API) listKeys(c *gin.Context) {
	ks, err := a.store.ListVirtualKeys(c)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, ks)
}

func (a *API) createKey(c *gin.Context) {
	var req createKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	plain, err := keys.Generate()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if req.ModelAllow == nil {
		req.ModelAllow = []string{}
	}
	if req.RouterAllow == nil {
		req.RouterAllow = []string{}
	}
	k := &store.VirtualKey{
		KeyHash: keys.HashKey(plain), Name: req.Name, Prefix: "cr",
		BudgetCents: req.BudgetCents, RateLimitRPM: req.RateLimitRPM,
		ModelAllow: req.ModelAllow, RouterAllow: req.RouterAllow,
	}
	if err := a.store.CreateVirtualKey(c, k); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "key.create", "virtual_key", k.ID, nil, store.Map{"name": k.Name})
	c.JSON(201, gin.H{"id": k.ID, "key": plain, "name": k.Name,
		"budget_cents": k.BudgetCents, "rate_limit_rpm": k.RateLimitRPM,
		"model_allowlist": k.ModelAllow, "router_allowlist": k.RouterAllow})
}

func (a *API) revokeKey(c *gin.Context) {
	if err := a.store.RevokeVirtualKey(c, c.Param("id")); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "key.revoke", "virtual_key", c.Param("id"), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) deleteKey(c *gin.Context) {
	id := c.Param("id")
	if err := a.store.DeleteVirtualKey(c, id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "key.delete", "virtual_key", id, nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

// ----- audit / system -----

// listAudit returns recent audit log entries.
// GAP-4 FIX: no longer calls store.Pool.Query directly — uses the proper
// store.ListAuditLog() method which keeps the Pool encapsulated.
func (a *API) listAudit(c *gin.Context) {
	entries, err := a.store.ListAuditLog(c, 200)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, entries)
}

// systemInfo returns runtime metadata about the control plane.
// L-8 IMPROVEMENT: now includes Cellar configuration presence and a summary of
// serving vs total routers for better operational visibility.
func (a *API) systemInfo(c *gin.Context) {
	snap := a.table.Snapshot()
	serving := len(snap)

	routers, _ := a.store.ListRouters(c)
	total := len(routers)
	healthy := 0
	for _, r := range routers {
		if r.HealthStatus == "healthy" {
			healthy++
		}
	}

	cellarConfigured := a.cfg.Cellar.Endpoint != "" &&
		a.cfg.Cellar.AccessKey != "" &&
		a.cfg.Cellar.SecretKey != ""

	c.JSON(200, gin.H{
		"environment":        a.cfg.Environment,
		"allowed_images":     a.cfg.AllowedImages,
		"routers_total":      total,
		"routers_serving":    serving,
		"routers_healthy":    healthy,
		"cellar_configured":  cellarConfigured,
		"admin_internal_addr": a.cfg.AdminInternalAddr,
	})
}

// ----- auth handlers -----

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (a *API) login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "username and password are required"})
		return
	}

	u, err := a.store.GetUserByUsername(c.Request.Context(), req.Username)
	if err != nil {
		c.JSON(401, gin.H{"error": "invalid username or password"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(401, gin.H{"error": "invalid username or password"})
		return
	}

	// Generate a high-entropy 32-byte session token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		c.JSON(500, gin.H{"error": "failed to generate session"})
		return
	}
	sessionToken := "cr_sess_" + hex.EncodeToString(tokenBytes)

	sess := &cache.SessionCache{
		UserID:   u.ID,
		Username: u.Username,
		Role:     u.Role,
	}

	// Store active session in Redis with 7-day TTL
	if err := a.cache.SetSession(c.Request.Context(), sessionToken, sess, 7*24*time.Hour); err != nil {
		log.Printf("[api] failed to cache session in redis: %v", err)
	}

	a.audit(c, "auth.login", "user", u.ID, nil, store.Map{"username": u.Username, "role": u.Role})

	c.JSON(200, gin.H{
		"token": sessionToken,
		"user": gin.H{
			"id":       u.ID,
			"username": u.Username,
			"email":    u.Email,
			"role":     u.Role,
		},
	})
}

func (a *API) logout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	tok := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if tok != "" {
		_ = a.cache.DelSession(c.Request.Context(), tok)
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) authMe(c *gin.Context) {
	actor, _ := c.Get("actor")
	role, _ := c.Get("role")
	userID, _ := c.Get("user_id")
	c.JSON(200, gin.H{
		"username": actor,
		"role":     role,
		"user_id":  userID,
	})
}

func (a *API) listLogs(c *gin.Context) {
	source := c.Query("source")
	level := c.Query("level")
	limitStr := c.Query("limit")
	limit := 100
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil {
			limit = n
		}
	}
	logs, err := a.store.ListSystemLogs(c.Request.Context(), source, level, limit)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, logs)
}

func (a *API) downloadLogs(c *gin.Context) {
	source := c.Query("source")
	level := c.Query("level")
	logs, err := a.store.ListSystemLogs(c.Request.Context(), source, level, 500)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=cleverroute-logs-%s.txt", time.Now().Format("20060102-150405")))
	c.Header("Content-Type", "text/plain; charset=utf-8")

	var b strings.Builder
	b.WriteString("================================================================================\n")
	b.WriteString(fmt.Sprintf("CleverRoute System & Aggregator Logs Export — %s\n", time.Now().UTC().Format(time.RFC1123)))
	b.WriteString(fmt.Sprintf("Filters: Level=%s Source=%s Total Entries=%d\n", level, source, len(logs)))
	b.WriteString("================================================================================\n\n")

	for i := len(logs) - 1; i >= 0; i-- {
		l := logs[i]
		metaBytes, _ := json.Marshal(l.Metadata)
		src := l.Source
		if l.RouterSlug != "" {
			src = l.Source + ":" + l.RouterSlug
		}
		b.WriteString(fmt.Sprintf("[%s] [%-5s] [%-16s] %s %s\n",
			l.TS.Format(time.RFC3339),
			l.Level,
			src,
			l.Message,
			string(metaBytes),
		))
	}

	c.String(200, b.String())
}

func (a *API) downloadAudit(c *gin.Context) {
	entries, err := a.store.ListAuditLog(c.Request.Context(), 500)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=cleverroute-audit-%s.txt", time.Now().Format("20060102-150405")))
	c.Header("Content-Type", "text/plain; charset=utf-8")

	var b strings.Builder
	b.WriteString("================================================================================\n")
	b.WriteString(fmt.Sprintf("CleverRoute Audit Log Export — %s\n", time.Now().UTC().Format(time.RFC1123)))
	b.WriteString(fmt.Sprintf("Total Audit Entries=%d\n", len(entries)))
	b.WriteString("================================================================================\n\n")

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		afterBytes, _ := json.Marshal(e.After)
		b.WriteString(fmt.Sprintf("[%s] Actor=%-10s Action=%-18s Target=%-20s Details=%s\n",
			e.Ts.Format(time.RFC3339),
			e.ActorEmail,
			e.Action,
			fmt.Sprintf("%s:%s", e.TargetType, e.TargetID),
			string(afterBytes),
		))
	}

	c.String(200, b.String())
}

// ----- helpers -----

func (a *API) audit(c *gin.Context, action, targetType, targetID string, before, after any) {
	actor, _ := c.Get("actor")
	actorStr := "admin"
	if s, ok := actor.(string); ok && s != "" {
		actorStr = s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = a.store.Audit(ctx, actorStr, action, targetType, targetID, toMap(before), toMap(after))

	// Broadcast audit event to Redis for real-time WebSocket clients
	auditEvent := store.Map{
		"id":          time.Now().UnixNano(),
		"ts":          time.Now().UTC(),
		"actor":       actorStr,
		"action":      action,
		"target_type": targetType,
		"target_id":   targetID,
		"after":       after,
	}
	if b, err := json.Marshal(auditEvent); err == nil {
		_ = a.cache.PublishEvent(ctx, "events:audit", string(b))
	}

	// Also record in central logger for unified persistence & live streaming
	logger.Info("audit", targetID, fmt.Sprintf("%s performed %s on %s", actorStr, action, targetType), toMap(after))
}

func toMap(v any) store.Map {
	if v == nil {
		return nil
	}
	if m, ok := v.(store.Map); ok {
		return m
	}
	b, err := json.Marshal(v)
	if err != nil {
		return store.Map{"_raw": "serialization error"}
	}
	var out store.Map
	if json.Unmarshal(b, &out) == nil {
		return out
	}
	return store.Map{"_raw": string(b)}
}

// ----- storage & cellar s3 management handlers -----

func (a *API) getStorageMetrics(c *gin.Context) {
	metrics := storage.CollectMetrics(a.bridge, a.cfg.VolumeScratchDir)
	c.JSON(http.StatusOK, metrics)
}

func (a *API) listLocalFiles(c *gin.Context) {
	targetPath := c.Query("path")
	if targetPath == "" {
		targetPath = a.cfg.VolumeScratchDir
	}

	items, err := a.explorer.ListLocalDirectory(targetPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (a *API) getLocalFile(c *gin.Context) {
	targetPath := c.Query("path")
	preview, err := a.explorer.ReadFilePreview(targetPath, 512*1024)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(preview))
}

func (a *API) downloadLocalFile(c *gin.Context) {
	targetPath := c.Query("path")
	safePath, err := a.explorer.ValidatePath(targetPath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(safePath)))
	c.File(safePath)
}

func (a *API) deleteLocalFile(c *gin.Context) {
	targetPath := c.Query("path")
	if err := a.explorer.DeleteLocalFile(targetPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "storage.local.delete", "file", targetPath, nil, store.Map{"path": targetPath})
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (a *API) listS3Objects(c *gin.Context) {
	if a.bridge == nil {
		c.JSON(http.StatusOK, []storage.S3ObjectItem{})
		return
	}
	prefix := c.Query("prefix")
	objects, err := a.explorer.ListS3Objects(c.Request.Context(), prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, objects)
}

func (a *API) downloadS3Object(c *gin.Context) {
	key := c.Query("key")
	if key == "" || a.bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key or S3 storage disabled"})
		return
	}

	obj, err := a.bridge.GetRawObject(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer obj.Close()

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(key)))
	c.Header("Content-Type", "application/octet-stream")
	_, _ = io.Copy(c.Writer, obj)
}

func (a *API) manualS3Sync(c *gin.Context) {
	if a.bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cellar S3 storage is not configured"})
		return
	}

	var req struct {
		RouterID string `json:"router_id"`
	}
	_ = c.ShouldBindJSON(&req)

	if req.RouterID != "" {
		r, err := a.findRouter(c, req.RouterID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "router not found"})
			return
		}
		localPath := filepath.Join(a.cfg.VolumeScratchDir, r.ID, "app_data")
		s3Key := fmt.Sprintf("namespaces/%s/app_data.tar.zst", r.ID)
		if err := a.bridge.StreamSnapshotToS3(c.Request.Context(), localPath, s3Key); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("sync failed: %v", err)})
			return
		}
		a.audit(c, "storage.s3.sync", "router", r.ID, nil, store.Map{"slug": r.Slug, "key": s3Key})
	} else {
		// StopAll / sync all running routers and logs
		a.manager.StopAll(c.Request.Context())
		if err := a.bridge.StreamSnapshotToS3(c.Request.Context(), "logs", "db/gateway_logs.tar.zst"); err != nil {
			log.Printf("[api] warning syncing logs to S3: %v", err)
		}
		a.audit(c, "storage.s3.sync_all", "system", "all", nil, nil)
	}

	c.JSON(http.StatusOK, gin.H{"status": "synchronized"})
}

func (a *API) manualS3Restore(c *gin.Context) {
	if a.bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cellar S3 storage is not configured"})
		return
	}

	var req struct {
		RouterID string `json:"router_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.RouterID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "router_id is required"})
		return
	}

	r, err := a.findRouter(c, req.RouterID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "router not found"})
		return
	}

	localPath := filepath.Join(a.cfg.VolumeScratchDir, r.ID, "app_data")
	s3Key := fmt.Sprintf("namespaces/%s/app_data.tar.zst", r.ID)
	if err := a.bridge.HydrateFromS3(c.Request.Context(), s3Key, localPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("restore failed: %v", err)})
		return
	}

	a.audit(c, "storage.s3.restore", "router", r.ID, nil, store.Map{"slug": r.Slug, "key": s3Key})
	c.JSON(http.StatusOK, gin.H{"status": "restored"})
}

func (a *API) deleteS3Object(c *gin.Context) {
	if a.bridge == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cellar S3 storage is not configured"})
		return
	}
	key := c.Query("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if err := a.explorer.DeleteS3Object(c.Request.Context(), key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a.audit(c, "storage.s3.delete", "object", key, nil, store.Map{"key": key})
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

