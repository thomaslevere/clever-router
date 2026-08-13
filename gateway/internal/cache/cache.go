package cache

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
}

func Open(ctx context.Context, addr, password string, db int) (*Cache, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		PoolSize:     16,
		MinIdleConns: 4,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Cache{rdb: rdb}, nil
}

func (c *Cache) Close() error { return c.rdb.Close() }
func (c *Cache) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// ----- routing table -----

func (c *Cache) SetRoute(ctx context.Context, slug, target string) error {
	return c.rdb.HSet(ctx, "routing", slug, target).Err()
}

func (c *Cache) GetRoute(ctx context.Context, slug string) (string, error) {
	v, err := c.rdb.HGet(ctx, "routing", slug).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (c *Cache) AllRoutes(ctx context.Context) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, "routing").Result()
}

func (c *Cache) DelRoute(ctx context.Context, slug string) error {
	return c.rdb.HDel(ctx, "routing", slug).Err()
}

// ----- virtual key cache -----

type VirtualKeyCache struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	BudgetCents  int64    `json:"budget_cents"`
	RateLimitRPM int      `json:"rate_limit_rpm"`
	ModelAllow   []string `json:"model_allowlist"`
	RouterAllow  []string `json:"router_allowlist"`
	Status       string   `json:"status"`
}

func (c *Cache) SetVirtualKey(ctx context.Context, hash string, k *VirtualKeyCache, ttl time.Duration) error {
	b, err := json.Marshal(k)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, "vkey:"+hash, b, ttl).Err()
}

func (c *Cache) GetVirtualKey(ctx context.Context, hash string) (*VirtualKeyCache, error) {
	v, err := c.rdb.Get(ctx, "vkey:"+hash).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var k VirtualKeyCache
	if err := json.Unmarshal([]byte(v), &k); err != nil {
		return nil, err
	}
	return &k, nil
}

func (c *Cache) DelVirtualKey(ctx context.Context, hash string) error {
	return c.rdb.Del(ctx, "vkey:"+hash).Err()
}

// ----- admin session cache -----

type SessionCache struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (c *Cache) SetSession(ctx context.Context, token string, s *SessionCache, ttl time.Duration) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, "sess:"+token, b, ttl).Err()
}

func (c *Cache) GetSession(ctx context.Context, token string) (*SessionCache, error) {
	v, err := c.rdb.Get(ctx, "sess:"+token).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s SessionCache
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Cache) DelSession(ctx context.Context, token string) error {
	return c.rdb.Del(ctx, "sess:"+token).Err()
}

// ----- rate limiting (atomic fixed-window via Lua) -----
//
// M-2 FIX: The previous INCR+EXPIRE pipeline was non-atomic — a crash between
// INCR and EXPIRE would leave the key without a TTL, permanently rate-limiting
// that key ID. This Lua script runs atomically on the Redis server: it increments
// the counter and sets the TTL only on the *first* increment (n == 1), so
// the window always expires correctly even if the server restarts mid-request.

var rateLimitLua = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// IncrRate atomically increments the request counter for the given key within
// a fixed sliding window of windowSec seconds and returns the new count.
func (c *Cache) IncrRate(ctx context.Context, key string, windowSec int) (int, error) {
	n, err := rateLimitLua.Run(ctx, c.rdb, []string{key}, windowSec).Int()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ----- circuit breaker / cooldown -----

func (c *Cache) SetCooldown(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Set(ctx, "cooldown:"+key, "1", ttl).Err()
}

func (c *Cache) IsCoolingDown(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, "cooldown:"+key).Result()
	return n > 0, err
}

// ----- pub/sub config reload -----

const ReloadChannel = "config:reload"

type ReloadEvent struct {
	Kind string `json:"kind"` // "route","vkey","router"
	Slug string `json:"slug"`
}

func (c *Cache) Publish(ctx context.Context, ev ReloadEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return c.rdb.Publish(ctx, ReloadChannel, b).Err()
}

// Subscribe returns a channel of ReloadEvents.
//
// BUG-4 FIX: The previous implementation exited permanently when the Redis
// connection dropped. This version wraps the subscription in a reconnect loop
// with exponential backoff so config:reload events keep propagating after a
// transient Redis restart without requiring a gateway restart.
func (c *Cache) Subscribe(ctx context.Context) <-chan *ReloadEvent {
	out := make(chan *ReloadEvent, 64)
	go func() {
		defer close(out)

		backoff := 500 * time.Millisecond
		const maxBackoff = 30 * time.Second

		for {
			if ctx.Err() != nil {
				return
			}

			pubsub := c.rdb.Subscribe(ctx, ReloadChannel)
			msgCh := pubsub.Channel()
			healthy := false

		drain:
			for {
				select {
				case <-ctx.Done():
					_ = pubsub.Close()
					return

				case msg, ok := <-msgCh:
					if !ok {
						// Channel closed — Redis connection dropped.
						break drain
					}
					healthy = true
					var ev ReloadEvent
					if err := json.Unmarshal([]byte(msg.Payload), &ev); err == nil {
						select {
						case out <- &ev:
						default:
							// Drop event if consumer is too slow rather than block.
							log.Printf("[cache] pub/sub: dropped event (channel full)")
						}
					}
				}
			}

			_ = pubsub.Close()

			// Reset backoff on a healthy session; ramp up on repeated failures.
			if healthy {
				backoff = 500 * time.Millisecond
			} else if backoff < maxBackoff {
				backoff *= 2
			}

			log.Printf("[cache] pub/sub connection lost, reconnecting in %v…", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}()
	return out
}
