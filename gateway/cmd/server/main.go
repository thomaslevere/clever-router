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
	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/runtime"
	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/storage"
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

	// Centralized Logging Hub (Disk file + Postgres + Redis PubSub + Stdout)
	logHub, err := logger.Init(st, c, "logs")
	if err != nil {
		log.Printf("warning: logger init: %v", err)
	} else {
		defer logHub.Close()
		logger.Info("system", "", "CleverRoute control plane booting", store.Map{"env": cfg.Environment, "port": cfg.HTTPPort})
	}

	// Secrets (AES-256-GCM envelope)
	box, err := secrets.New(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("secrets: %v", err)
	}

	// S3 FastVolumeBridge (Clever Cloud Cellar / S3)
	var bridge *storage.FastVolumeBridge
	if cfg.HasCellar() {
		b, err := storage.NewFastVolumeBridge(cfg.Cellar.Endpoint, cfg.Cellar.AccessKey, cfg.Cellar.SecretKey, cfg.Cellar.Bucket, cfg.Cellar.Region, cfg.Cellar.UseSSL)
		if err != nil {
			log.Printf("warning: failed to initialize S3 FastVolumeBridge: %v", err)
		} else {
			bridge = b
			log.Printf("FastVolumeBridge connected to Cellar S3 (endpoint: %s, bucket: %s, region: %s)", cfg.Cellar.Endpoint, cfg.Cellar.Bucket, cfg.Cellar.Region)
		}
	} else {
		log.Println("S3 FastVolumeBridge disabled (no Cellar/S3 credentials configured; using local disk volumes)")
	}

	// Adapter registry
	reg := adapters.NewRegistry(adapters.OmniRouteAdapter{})

	// Hot routing table, seeded from Redis
	table := router.NewTable(c)
	if err := table.Load(rootCtx); err != nil {
		log.Printf("warning: load routing table: %v", err)
	}

	// Docker-socket runtime manager
	mgr, err := adapters.NewManager(st, c, box, reg, cfg.AllowedImages, table, bridge, cfg.VolumeScratchDir)
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
		Cfg: cfg, Store: st, Cache: c, Box: box, Manager: mgr, Table: table, Bridge: bridge,
	})
	a.Register(r)

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Pre-flight: Auto-Restore S3 Snapshots to local scratch before booting
	if bridge != nil {
		restoreCtx, cancelRestore := context.WithTimeout(rootCtx, 45*time.Second)
		if err := bridge.AutoRestoreFromS3(restoreCtx, cfg.VolumeScratchDir); err != nil {
			log.Printf("[boot] warning: auto-restore from S3: %v", err)
		}
		cancelRestore()
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
	sig := <-quit
	log.Printf("==> Termination signal %s received. Executing zero-loss graceful teardown...", sig.String())

	// 1. Stop accepting inbound HTTP traffic
	shutdownHttpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	_ = srv.Shutdown(shutdownHttpCtx)

	// 2. Stop running router containers and flush their volumes to S3
	shutdownFlushCtx, flushCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer flushCancel()
	mgr.StopAll(shutdownFlushCtx)

	// 3. Final S3 flush of all router namespaces & logs
	if bridge != nil {
		log.Println("[shutdown] Flushing all router volumes and system snapshots to Cellar S3...")
		if err := mgr.SyncAllToS3(shutdownFlushCtx); err != nil {
			log.Printf("[shutdown] warning: S3 sync error: %v", err)
		} else {
			log.Println("[shutdown] All router namespaces successfully backed up to S3.")
		}
	}

	rootCancel()
	log.Println("==> Graceful shutdown completed cleanly. Exiting.")
}
