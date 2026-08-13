-- CleverRoute system logs table & retention management.
BEGIN;

CREATE TABLE IF NOT EXISTS system_logs (
    id          BIGSERIAL PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    level       TEXT NOT NULL DEFAULT 'INFO',        -- INFO, WARN, ERROR, DEBUG
    source      TEXT NOT NULL DEFAULT 'system',      -- proxy, router, auth, supervisor, adapter
    router_slug TEXT NOT NULL DEFAULT '',
    message     TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_system_logs_ts ON system_logs (ts DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_source_ts ON system_logs (source, ts DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_level_ts ON system_logs (level, ts DESC);

-- Update prune_old_data() to also delete system logs older than 30 days.
CREATE OR REPLACE FUNCTION prune_old_data() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    -- Keep 7 days of health checks
    DELETE FROM health_checks WHERE ts < now() - INTERVAL '7 days';

    -- Keep 90 days of usage data for quota & billing records
    DELETE FROM usage WHERE ts < now() - INTERVAL '90 days';

    -- Keep 30 days of system logs
    DELETE FROM system_logs WHERE ts < now() - INTERVAL '30 days';
END;
$$;

COMMIT;
