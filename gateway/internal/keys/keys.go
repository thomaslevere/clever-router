package keys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/store"
)

const prefix = "cr-"

// Auth resolves a bearer token to a cached virtual key. The plaintext key is
// never stored; only its SHA-256 hash is persisted and cached.
type Auth struct {
	store *store.Store
	cache *cache.Cache
}

func NewAuth(st *store.Store, c *cache.Cache) *Auth {
	return &Auth{store: st, cache: c}
}

// HashKey returns the hex SHA-256 of a plaintext virtual key.
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Generate creates a new plaintext virtual key (shown once to the user).
func Generate() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

type KeyInfo struct {
	ID          string
	Name        string
	BudgetCents int64
	RateLimitRPM int
	ModelAllow  []string
	RouterAllow []string
	Status      string
}

// Authenticate hashes the bearer token and resolves it via Redis (hot) → Postgres (cold).
func (a *Auth) Authenticate(ctx context.Context, bearer string) (*KeyInfo, error) {
	plain := strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer "))
	plain = strings.TrimPrefix(plain, prefix)
	if plain == "" {
		return nil, ErrNoKey
	}
	full := prefix + plain
	hash := HashKey(full)

	cached, err := a.cache.GetVirtualKey(ctx, hash)
	if err != nil {
		return nil, err
	}
	if cached == nil {
		// cold path: load from Postgres, warm the cache
		row := a.store.Pool.QueryRow(ctx, `
			SELECT id, name, budget_cents, rate_limit_rpm, model_allowlist, router_allowlist, status
			FROM virtual_keys WHERE key_hash=$1 AND status='active'`, hash)
		var k KeyInfo
		if err := row.Scan(&k.ID, &k.Name, &k.BudgetCents, &k.RateLimitRPM, &k.ModelAllow, &k.RouterAllow, &k.Status); err != nil {
			return nil, ErrInvalidKey
		}
		_ = a.cache.SetVirtualKey(ctx, hash, &cache.VirtualKeyCache{
			ID: k.ID, Name: k.Name, BudgetCents: k.BudgetCents, RateLimitRPM: k.RateLimitRPM,
			ModelAllow: k.ModelAllow, RouterAllow: k.RouterAllow, Status: k.Status,
		}, 10*time.Minute)
		cached = &cache.VirtualKeyCache{
			ID: k.ID, Name: k.Name, BudgetCents: k.BudgetCents, RateLimitRPM: k.RateLimitRPM,
			ModelAllow: k.ModelAllow, RouterAllow: k.RouterAllow, Status: k.Status,
		}
	}
	if cached.Status != "active" {
		return nil, ErrRevoked
	}
	return &KeyInfo{
		ID: cached.ID, Name: cached.Name, BudgetCents: cached.BudgetCents,
		RateLimitRPM: cached.RateLimitRPM, ModelAllow: cached.ModelAllow,
		RouterAllow: cached.RouterAllow, Status: cached.Status,
	}, nil
}

// AllowModel reports whether the model is permitted by the key's allowlist.
// An empty allowlist means all models are allowed.
func AllowModel(allow []string, model string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, m := range allow {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}

// AllowRouter reports whether the router slug is permitted by the key's allowlist.
func AllowRouter(allow []string, slug string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, s := range allow {
		if strings.EqualFold(s, slug) {
			return true
		}
	}
	return false
}

// CheckRate enforces the fixed-window RPM limit using Redis. rpm==0 means unlimited.
func (a *Auth) CheckRate(ctx context.Context, keyID string, rpm int) error {
	if rpm <= 0 {
		return nil
	}
	n, err := a.cache.IncrRate(ctx, "rl:"+keyID, 60)
	if err != nil {
		return err
	}
	if n > rpm {
		return ErrRateLimited
	}
	return nil
}

var (
	ErrNoKey       = fmt.Errorf("missing api key")
	ErrInvalidKey  = fmt.Errorf("invalid api key")
	ErrRevoked     = fmt.Errorf("revoked api key")
	ErrRateLimited = fmt.Errorf("rate limit exceeded")
)
