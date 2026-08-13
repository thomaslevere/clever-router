package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.Pool.Ping(ctx)
}

// User is an administrator account for accessing the control plane.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// GetUserByUsername retrieves a user by their username or email (case-insensitive).
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `
		SELECT id, COALESCE(username, email), email, password_hash, role, created_at
		FROM users
		WHERE LOWER(username) = LOWER($1) OR LOWER(email) = LOWER($1)
		LIMIT 1`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Router is the managed-resource representation of an AI router instance.
type Router struct {
	ID             string     `json:"id"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	AdapterType    string     `json:"adapter_type"`
	ImageRef       string     `json:"image_ref"`
	DesiredVersion string     `json:"desired_version"`
	CurrentVersion string     `json:"current_version"`
	EndpointPath   string     `json:"endpoint_path"`
	NativePanelURL string     `json:"native_panel_url"`
	DesiredState   string     `json:"desired_state"`
	RuntimeState   string     `json:"runtime_state"`
	TargetAddr     string     `json:"target_addr"`
	ContainerID    string     `json:"container_id"`
	Config         Map        `json:"config"`
	ProvidersCount int        `json:"providers_count"`
	ModelsCount    int        `json:"models_count"`
	HealthStatus   string     `json:"health_status"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Map = map[string]any

func (s *Store) ListRouters(ctx context.Context) ([]Router, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, slug, name, adapter_type, image_ref, desired_version, current_version,
		       endpoint_path, native_panel_url, desired_state, runtime_state, target_addr,
		       container_id, config, providers_count, models_count, health_status,
		       last_seen_at, created_at, updated_at
		FROM routers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Router{}
	for rows.Next() {
		r, err := scanRouter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRouter(ctx context.Context, id string) (*Router, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, slug, name, adapter_type, image_ref, desired_version, current_version,
		       endpoint_path, native_panel_url, desired_state, runtime_state, target_addr,
		       container_id, config, providers_count, models_count, health_status,
		       last_seen_at, created_at, updated_at
		FROM routers WHERE id = $1`, id)
	r, err := scanRouter(row)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetRouterBySlug(ctx context.Context, slug string) (*Router, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, slug, name, adapter_type, image_ref, desired_version, current_version,
		       endpoint_path, native_panel_url, desired_state, runtime_state, target_addr,
		       container_id, config, providers_count, models_count, health_status,
		       last_seen_at, created_at, updated_at
		FROM routers WHERE slug = $1`, slug)
	r, err := scanRouter(row)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) CreateRouter(ctx context.Context, r *Router) error {
	return s.Pool.QueryRow(ctx, `
		INSERT INTO routers (slug, name, adapter_type, image_ref, desired_version, endpoint_path, desired_state, config)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, created_at, updated_at`,
		r.Slug, r.Name, r.AdapterType, r.ImageRef, r.DesiredVersion, r.EndpointPath,
		r.DesiredState, r.Config).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
}

func (s *Store) UpdateRouterState(ctx context.Context, id, runtimeState, targetAddr, containerID, nativePanelURL string, health string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE routers SET runtime_state=$2, target_addr=$3, container_id=$4, native_panel_url=$5,
		                   health_status=$6, last_seen_at=now(), updated_at=now()
		WHERE id=$1`,
		id, runtimeState, targetAddr, containerID, nativePanelURL, health)
	return err
}

func (s *Store) UpdateRouterCounts(ctx context.Context, id string, providers, models int) error {
	_, err := s.Pool.Exec(ctx, `UPDATE routers SET providers_count=$2, models_count=$3, updated_at=now() WHERE id=$1`,
		id, providers, models)
	return err
}

func (s *Store) SetDesiredState(ctx context.Context, id, state string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE routers SET desired_state=$2, updated_at=now() WHERE id=$1`, id, state)
	return err
}

func (s *Store) UpdateRouter(ctx context.Context, id, name string, config Map) error {
	_, err := s.Pool.Exec(ctx, `UPDATE routers SET name=$2, config=$3, updated_at=now() WHERE id=$1`, id, name, config)
	return err
}

func (s *Store) DeleteRouter(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM routers WHERE id=$1`, id)
	return err
}

type Scanner interface {
	Scan(dest ...any) error
}

func scanRouter(sc Scanner) (Router, error) {
	var r Router
	var lastSeen *time.Time
	err := sc.Scan(
		&r.ID, &r.Slug, &r.Name, &r.AdapterType, &r.ImageRef, &r.DesiredVersion, &r.CurrentVersion,
		&r.EndpointPath, &r.NativePanelURL, &r.DesiredState, &r.RuntimeState, &r.TargetAddr,
		&r.ContainerID, &r.Config, &r.ProvidersCount, &r.ModelsCount, &r.HealthStatus,
		&lastSeen, &r.CreatedAt, &r.UpdatedAt,
	)
	r.LastSeenAt = lastSeen
	return r, err
}

// ProviderCredential holds an encrypted API key for a router+provider pair.
type ProviderCredential struct {
	ID             string     `json:"id"`
	RouterID       string     `json:"router_id"`
	Provider       string     `json:"provider"`
	Ciphertext     []byte     `json:"-"`
	KeyID          string     `json:"key_id"`
	Metadata       Map        `json:"metadata"`
	LastVerifiedAt *time.Time `json:"last_verified_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *Store) SaveCredential(ctx context.Context, c *ProviderCredential) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO provider_credentials (router_id, provider, ciphertext, key_id, metadata)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (router_id, provider) DO UPDATE SET ciphertext=EXCLUDED.ciphertext, key_id=EXCLUDED.key_id, metadata=EXCLUDED.metadata`,
		c.RouterID, c.Provider, c.Ciphertext, c.KeyID, c.Metadata)
	return err
}

func (s *Store) ListCredentials(ctx context.Context, routerID string) ([]ProviderCredential, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, router_id, provider, key_id, metadata, last_verified_at, created_at
		FROM provider_credentials WHERE router_id=$1 ORDER BY provider`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderCredential{}
	for rows.Next() {
		var c ProviderCredential
		if err := rows.Scan(&c.ID, &c.RouterID, &c.Provider, &c.KeyID, &c.Metadata, &c.LastVerifiedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCredentialByProvider(ctx context.Context, routerID, provider string) (*ProviderCredential, error) {
	var c ProviderCredential
	err := s.Pool.QueryRow(ctx, `
		SELECT id, router_id, provider, ciphertext, key_id, metadata, last_verified_at, created_at
		FROM provider_credentials WHERE router_id=$1 AND provider=$2`, routerID, provider).
		Scan(&c.ID, &c.RouterID, &c.Provider, &c.Ciphertext, &c.KeyID, &c.Metadata, &c.LastVerifiedAt, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteCredentialByProvider removes a credential by router+provider pair
// (semantically correct REST path: DELETE /routers/:id/credentials/:provider).
// GAP-5 FIX: previously only DeleteCredential(id) existed, leaving the UI
// calling /credentials/:id which is inconsistent with REST conventions.
func (s *Store) DeleteCredentialByProvider(ctx context.Context, routerID, provider string) error {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM provider_credentials WHERE router_id=$1 AND provider=$2`, routerID, provider)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("credential not found for router %s provider %s", routerID, provider)
	}
	return nil
}

// VirtualKey is a client-facing API key with optional budget and rate limits.
type VirtualKey struct {
	ID           string     `json:"id"`
	KeyHash      string     `json:"-"`
	Name         string     `json:"name"`
	OwnerID      *string    `json:"owner_id"`
	Prefix       string     `json:"prefix"`
	BudgetCents  int64      `json:"budget_cents"`
	SpentCents   int64      `json:"spent_cents"`
	RateLimitRPM int        `json:"rate_limit_rpm"`
	ModelAllow   []string   `json:"model_allowlist"`
	RouterAllow  []string   `json:"router_allowlist"`
	Status       string     `json:"status"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (s *Store) CreateVirtualKey(ctx context.Context, k *VirtualKey) error {
	return s.Pool.QueryRow(ctx, `
		INSERT INTO virtual_keys (key_hash, name, owner_id, prefix, budget_cents, rate_limit_rpm, model_allowlist, router_allowlist)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		k.KeyHash, k.Name, k.OwnerID, k.Prefix, k.BudgetCents, k.RateLimitRPM, k.ModelAllow, k.RouterAllow).
		Scan(&k.ID, &k.CreatedAt)
}

func (s *Store) ListVirtualKeys(ctx context.Context) ([]VirtualKey, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, prefix, budget_cents, spent_cents, rate_limit_rpm, model_allowlist, router_allowlist, status, last_used_at, created_at
		FROM virtual_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VirtualKey{}
	for rows.Next() {
		var k VirtualKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.BudgetCents, &k.SpentCents, &k.RateLimitRPM, &k.ModelAllow, &k.RouterAllow, &k.Status, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) RevokeVirtualKey(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE virtual_keys SET status='revoked', revoked_at=now() WHERE id=$1`, id)
	return err
}

// CheckBudget returns true if the key has remaining budget (or no budget limit).
// BUG-2 FIX: the proxy now calls this before forwarding to enforce budget_cents.
func (s *Store) CheckBudget(ctx context.Context, keyID string) (bool, error) {
	var budgetCents, spentCents int64
	err := s.Pool.QueryRow(ctx,
		`SELECT budget_cents, spent_cents FROM virtual_keys WHERE id=$1`, keyID).
		Scan(&budgetCents, &spentCents)
	if err != nil {
		// On DB error, fail open (allow request) to avoid false positives.
		return true, err
	}
	return budgetCents == 0 || spentCents < budgetCents, nil
}

// IncrSpentCents atomically adds cents to a key's spent_cents and updates
// last_used_at. Called asynchronously after each request.
func (s *Store) IncrSpentCents(ctx context.Context, keyID string, cents int64) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE virtual_keys SET spent_cents = spent_cents + $2, last_used_at = now() WHERE id = $1`,
		keyID, cents)
	return err
}

func (s *Store) RecordUsage(ctx context.Context, keyID, routerID, routerSlug, model, provider string, prompt, completion, total int, costCents int64, status int) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO usage (virtual_key_id, router_id, router_slug, model, provider, prompt_tokens, completion_tokens, total_tokens, cost_cents, status_code)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, $3,$4,$5,$6,$7,$8,$9,$10)`,
		keyID, routerID, routerSlug, model, provider, prompt, completion, total, costCents, status)
	return err
}

func (s *Store) InsertHealthCheck(ctx context.Context, routerID, status string, latencyMs int, detail string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO health_checks (router_id, status, latency_ms, detail) VALUES ($1,$2,$3,$4)`,
		routerID, status, latencyMs, detail)
	return err
}

func (s *Store) Audit(ctx context.Context, actorEmail, action, targetType, targetID string, before, after Map) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO audit_log (actor_email, action, target_type, target_id, before, "after") VALUES ($1,$2,$3,$4,$5,$6)`,
		actorEmail, action, targetType, targetID, before, after)
	return err
}

// AuditEntry is a single row from the audit_log table.
type AuditEntry struct {
	ID         int64     `json:"id"`
	ActorEmail string    `json:"actor"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	After      Map       `json:"after"`
	Ts         time.Time `json:"ts"`
}

// ListAuditLog returns the most recent audit entries.
// GAP-4 FIX: previously this query lived inline in api.go directly on
// store.Pool, breaking the abstraction and preventing testing.
func (s *Store) ListAuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, actor_email, action, target_type, target_id, "after", ts
		FROM audit_log ORDER BY ts DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorEmail, &e.Action, &e.TargetType, &e.TargetID, &e.After, &e.Ts); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Model is a discovered AI model associated with a router.
type Model struct {
	ID         string    `json:"id"`
	RouterID   string    `json:"router_id"`
	ModelID    string    `json:"model_id"`
	Provider   string    `json:"provider"`
	Modalities string    `json:"modalities"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

func (s *Store) UpsertModels(ctx context.Context, routerID string, models []Model) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM models WHERE router_id=$1`, routerID); err != nil {
		return err
	}
	for _, m := range models {
		if _, err := tx.Exec(ctx, `
			INSERT INTO models (router_id, model_id, provider, modalities)
			VALUES ($1,$2,$3,$4) ON CONFLICT (router_id, model_id) DO UPDATE SET provider=EXCLUDED.provider, modalities=EXCLUDED.modalities, last_seen_at=now()`,
			routerID, m.ModelID, m.Provider, m.Modalities); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListModels(ctx context.Context, routerID string) ([]Model, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, router_id, model_id, provider, modalities, last_seen_at FROM models WHERE router_id=$1 ORDER BY model_id`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Model{}
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.RouterID, &m.ModelID, &m.Provider, &m.Modalities, &m.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SystemLog represents a structured event recorded in the centralized log system.
type SystemLog struct {
	ID         int64     `json:"id"`
	TS         time.Time `json:"ts"`
	Level      string    `json:"level"`
	Source     string    `json:"source"`
	RouterSlug string    `json:"router_slug,omitempty"`
	Message    string    `json:"message"`
	Metadata   Map       `json:"metadata"`
}

func (s *Store) InsertSystemLog(ctx context.Context, l *SystemLog) error {
	if l.Metadata == nil {
		l.Metadata = Map{}
	}
	return s.Pool.QueryRow(ctx, `
		INSERT INTO system_logs (level, source, router_slug, message, metadata)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, ts`,
		l.Level, l.Source, l.RouterSlug, l.Message, l.Metadata).
		Scan(&l.ID, &l.TS)
}

func (s *Store) ListSystemLogs(ctx context.Context, source, level string, limit int) ([]SystemLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `
		SELECT id, ts, level, source, router_slug, message, metadata
		FROM system_logs
		WHERE ($1 = '' OR source = $1)
		  AND ($2 = '' OR level = $2)
		ORDER BY ts DESC
		LIMIT $3`
	rows, err := s.Pool.Query(ctx, query, source, level, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SystemLog{}
	for rows.Next() {
		var l SystemLog
		if err := rows.Scan(&l.ID, &l.TS, &l.Level, &l.Source, &l.RouterSlug, &l.Message, &l.Metadata); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

