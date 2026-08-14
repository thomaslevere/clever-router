package runtime

import (
	"context"
	"log"
	"time"

	"github.com/clever-route/gateway/internal/adapters"
	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/store"
)

// Supervisor reconciles desired router state with running containers.
// On boot it restarts every router whose desired_state is "running" so the
// routes come back even after a Clever Cloud container/VM restart.
type Supervisor struct {
	manager *adapters.Manager
	store   *store.Store
	cache   *cache.Cache
	table   *router.Table
}

func NewSupervisor(m *adapters.Manager, st *store.Store, c *cache.Cache, t *router.Table) *Supervisor {
	return &Supervisor{manager: m, store: st, cache: c, table: t}
}

// Boot reconciles desired state on startup. Each router is started in its own
// goroutine so a slow image pull or unhealthy router doesn't block others.
func (s *Supervisor) Boot(ctx context.Context) {
	routers, err := s.store.ListRouters(ctx)
	if err != nil {
		log.Printf("[supervisor] list routers: %v", err)
		return
	}
	for _, r := range routers {
		if r.DesiredState == "running" {
			go func(r store.Router) {
				log.Printf("[supervisor] starting router %s (%s)", r.Slug, r.ImageRef)
				if err := s.manager.Start(ctx, &r); err != nil {
					log.Printf("[supervisor] failed to start %s: %v", r.Slug, err)
				}
			}(r)
		} else {
			s.table.Delete(r.Slug)
		}
	}
}

// Listen consumes config reload events and applies them to the in-memory table
// so every gateway instance stays hot in sync after an admin change.
func (s *Supervisor) Listen(ctx context.Context) {
	ch := s.cache.Subscribe(ctx)
	for ev := range ch {
		r, err := s.store.GetRouterBySlug(ctx, ev.Slug)
		if err != nil || r == nil {
			s.table.Delete(ev.Slug)
			continue
		}
		if r.RuntimeState == "running" && r.TargetAddr != "" {
			s.table.Set(r.Slug, r.TargetAddr)
		} else {
			s.table.Delete(r.Slug)
		}
	}
}

// Checker periodically health-checks serving routers and prunes old data.
type Checker struct {
	manager       *adapters.Manager
	store         *store.Store
	healthInterval time.Duration
	pruneInterval  time.Duration
}

func NewChecker(m *adapters.Manager, st *store.Store, interval time.Duration) *Checker {
	return &Checker{
		manager:       m,
		store:         st,
		healthInterval: interval,
		pruneInterval:  time.Hour,
	}
}

func (c *Checker) Run(ctx context.Context) {
	healthTick := time.NewTicker(c.healthInterval)
	pruneTick := time.NewTicker(c.pruneInterval)
	defer healthTick.Stop()
	defer pruneTick.Stop()

	// Run an initial prune on startup to clear stale data immediately.
	go c.prune(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-healthTick.C:
			c.tick(ctx)
		case <-pruneTick.C:
			go c.prune(ctx)
		}
	}
}

func (c *Checker) tick(ctx context.Context) {
	routers, err := c.store.ListRouters(ctx)
	if err != nil {
		return
	}
	for _, r := range routers {
		if r.RuntimeState != "running" && r.RuntimeState != "unhealthy" {
			continue
		}
		if r.TargetAddr == "" {
			continue
		}
		go func(r store.Router) {
			if err := c.manager.HealthCheck(ctx, &r); err != nil {
				log.Printf("[health] %s: %v", r.Slug, err)
			} else if r.RuntimeState == "running" {
				_ = c.manager.Snapshot(ctx, &r)
			}
		}(r)
	}
}

// prune calls the SQL prune_old_data() function to enforce retention policies
// on health_checks (7 days) and usage (90 days) tables, preventing unbounded growth.
func (c *Checker) prune(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := c.store.Pool.Exec(ctx, `SELECT prune_old_data()`); err != nil {
		log.Printf("[checker] prune: %v", err)
	}
}
