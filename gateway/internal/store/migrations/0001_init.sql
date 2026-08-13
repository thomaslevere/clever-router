-- CleverRoute control-plane schema (system of record)
BEGIN;

CREATE TABLE IF NOT EXISTS schema_migrations (
    id          TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('owner','admin','viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS routers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    adapter_type    TEXT NOT NULL,
    image_ref       TEXT NOT NULL,
    desired_version TEXT NOT NULL DEFAULT 'latest',
    current_version TEXT NOT NULL DEFAULT '',
    endpoint_path   TEXT NOT NULL,              -- e.g. /omniroute
    native_panel_url TEXT NOT NULL DEFAULT '',   -- discovered, e.g. http://<addr>:20128/dashboard
    desired_state   TEXT NOT NULL DEFAULT 'stopped' CHECK (desired_state IN ('running','stopped')),
    runtime_state   TEXT NOT NULL DEFAULT 'stopped' CHECK (runtime_state IN ('running','starting','stopped','failed','unhealthy')),
    target_addr     TEXT NOT NULL DEFAULT '',     -- hot target, e.g. http://172.17.0.5:20128
    container_id    TEXT NOT NULL DEFAULT '',
    config          JSONB NOT NULL DEFAULT '{}'::jsonb,
    providers_count INT  NOT NULL DEFAULT 0,
    models_count    INT  NOT NULL DEFAULT 0,
    health_status   TEXT NOT NULL DEFAULT 'unknown' CHECK (health_status IN ('healthy','unhealthy','unknown')),
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provider_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    router_id       UUID NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
    provider        TEXT NOT NULL,
    ciphertext      BYTEA NOT NULL,
    key_id          TEXT NOT NULL,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_verified_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (router_id, provider)
);

CREATE TABLE IF NOT EXISTS virtual_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_hash        TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    owner_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    prefix          TEXT NOT NULL DEFAULT 'cr',
    budget_cents    BIGINT NOT NULL DEFAULT 0,    -- 0 = unlimited
    spent_cents     BIGINT NOT NULL DEFAULT 0,
    rate_limit_rpm  INT     NOT NULL DEFAULT 0,  -- 0 = unlimited
    model_allowlist JSONB NOT NULL DEFAULT '[]'::jsonb,
    router_allowlist JSONB NOT NULL DEFAULT '[]'::jsonb, -- empty = all routers
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS usage (
    id              BIGSERIAL PRIMARY KEY,
    virtual_key_id  UUID REFERENCES virtual_keys(id) ON DELETE CASCADE,
    router_id       UUID REFERENCES routers(id) ON DELETE SET NULL,
    router_slug     TEXT NOT NULL,
    model           TEXT NOT NULL,
    provider        TEXT NOT NULL DEFAULT '',
    prompt_tokens   INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    total_tokens    INT NOT NULL DEFAULT 0,
    cost_cents      BIGINT NOT NULL DEFAULT 0,
    status_code     INT NOT NULL DEFAULT 200,
    ts              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_usage_key_ts ON usage(virtual_key_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_usage_router_ts ON usage(router_id, ts DESC);

CREATE TABLE IF NOT EXISTS models (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    router_id       UUID NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
    model_id        TEXT NOT NULL,
    provider        TEXT NOT NULL DEFAULT '',
    modalities      TEXT NOT NULL DEFAULT '',
    raw             JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (router_id, model_id)
);

CREATE TABLE IF NOT EXISTS deployments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    router_id       UUID NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
    from_version    TEXT NOT NULL DEFAULT '',
    to_version      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','succeeded','failed','rolled_back')),
    detail          TEXT NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_deployments_router ON deployments(router_id, started_at DESC);

CREATE TABLE IF NOT EXISTS health_checks (
    id              BIGSERIAL PRIMARY KEY,
    router_id       UUID NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
    status          TEXT NOT NULL,                -- healthy/unhealthy
    latency_ms      INT NOT NULL DEFAULT 0,
    detail          TEXT NOT NULL DEFAULT '',
    ts              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_health_router_ts ON health_checks(router_id, ts DESC);

CREATE TABLE IF NOT EXISTS audit_log (
    id              BIGSERIAL PRIMARY KEY,
    actor_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_email     TEXT NOT NULL DEFAULT 'system',
    action          TEXT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       TEXT NOT NULL DEFAULT '',
    before          JSONB,
    "after"         JSONB,
    ts              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);

COMMIT;
