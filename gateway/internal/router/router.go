package router

import (
	"context"
	"sync"

	"github.com/clever-route/gateway/internal/cache"
)

// Table is an in-memory routing table (slug -> target URL) kept hot from Redis.
// It is refreshed via Pub/Sub so an admin edit propagates to every gateway
// instance in well under a millisecond without a restart.
type Table struct {
	cache *cache.Cache
	mu    sync.RWMutex
	m     map[string]string
}

func NewTable(c *cache.Cache) *Table {
	return &Table{cache: c, m: make(map[string]string)}
}

// Load seeds the table from Redis. Called once on boot.
func (t *Table) Load(ctx context.Context) error {
	all, err := t.cache.AllRoutes(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.m = all
	t.mu.Unlock()
	return nil
}

func (t *Table) Set(slug, target string) {
	t.mu.Lock()
	t.m[slug] = target
	t.mu.Unlock()
}

func (t *Table) Delete(slug string) {
	t.mu.Lock()
	delete(t.m, slug)
	t.mu.Unlock()
}

// Lookup returns the target URL for a slug. The returned ok bool is true when a
// route exists and has a non-empty target (i.e. the router is serving).
func (t *Table) Lookup(slug string) (target string, ok bool) {
	t.mu.RLock()
	target = t.m[slug]
	t.mu.RUnlock()
	return target, target != ""
}

// Snapshot returns a copy of the table.
func (t *Table) Snapshot() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]string, len(t.m))
	for k, v := range t.m {
		out[k] = v
	}
	return out
}
