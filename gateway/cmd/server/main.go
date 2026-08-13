package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/clever-route/gateway/internal/adapters"
	"github.com/clever-route/gateway/internal/api"
	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/config"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/runtime"
	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Postgres (system of record)
	st, err := store.Open(rootCtx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(rootCtx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied")

	// Redis (hot cache + pub/sub)
	c, err := cache.Open(rootCtx, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer c.Close()

	// Secrets (AES-256-GCM envelope)
	box, err := secrets.New(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("secrets: %v", err)
	}

	// Adapter registry
	reg := adapters.NewRegistry(adapters.OmniRouteAdapter{})

	// Hot routing table, seeded from Redis
	table := router.NewTable(c)
	if err := table.Load(rootCtx); err != nil {
		log.Printf("warning: load routing table: %v", err)
	}

	// Docker-socket runtime manager
	mgr, err := adapters.NewManager(st, c, box, reg, cfg.AllowedImages, table)
	if err != nil {
		log.Fatalf("manager: %v", err)
	}
	defer mgr.Close()

	// Supervisor + health checker
	sup := runtime.NewSupervisor(mgr, st, c, table)
	checker := runtime.NewChecker(mgr, st, 30*time.Second)

	// HTTP engine
	if cfg.IsDev() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	if cfg.IsDev() {
		r.Use(cors.New(cors.Config{
			AllowAllOrigins:  true,
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"*"},
			ExposeHeaders:    []string{"X-CleverRoute-Router", "X-CleverRoute-Model"},
			AllowCredentials: false,
		}))
	}

	a := api.New(api.Deps{
		Cfg: cfg, Store: st, Cache: c, Box: box, Manager: mgr, Table: table,
	})
	a.Register(r)

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Background workers
	go sup.Listen(rootCtx)
	go checker.Run(rootCtx)

	// Reconcile desired state on boot (restart routers after a Clever Cloud restart).
	go func() {
		time.Sleep(500 * time.Millisecond) // let the server bind first
		sup.Boot(rootCtx)
	}()

	go func() {
		log.Printf("listening on :%s", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	rootCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
