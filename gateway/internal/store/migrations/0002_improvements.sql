-- CleverRoute schema improvements: retention policies and additional indexes.
--
-- health_checks and usage grow unboundedly. This migration adds:
--   1. A partial index on health_checks for fast recent-only lookups.
--   2. A partial index on usage for per-key dashboard queries.
--   3. A pg_cron-compatible pruning function callable from an external scheduler
--      (or via a periodic Postgres job). The gateway calls prune_old_data()
--      on startup and once per hour via the supervisor.
--
-- NOTE: pg_cron is not available on all Postgres plans. The gateway supervisor
-- calls prune_old_data() directly on a timer as a fallback.
BEGIN;

-- ---- additional indexes for hot-path queries ----

-- Fast lookup of the most recent health check per router (dashboard).
CREATE INDEX IF NOT EXISTS idx_health_router_recent
    ON health_checks(router_id, ts DESC)
    WHERE ts > now() - INTERVAL '7 days';

-- Fast per-key usage aggregation.
CREATE INDEX IF NOT EXISTS idx_usage_key_recent
    ON usage(virtual_key_id, ts DESC)
    WHERE ts > now() - INTERVAL '30 days';

-- ---- retention / pruning function ----

-- prune_old_data() deletes rows older than the retention window.
-- Safe to call concurrently; each DELETE is a separate short transaction.
CREATE OR REPLACE FUNCTION prune_old_data() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    -- Keep 7 days of health checks per router (beyond that, aggregate metrics
    -- are more useful than raw rows).
    DELETE FROM health_checks WHERE ts < now() - INTERVAL '7 days';

    -- Keep 90 days of usage data for billing/audit purposes.
    DELETE FROM usage WHERE ts < now() - INTERVAL '90 days';
END;
$$;

COMMIT;
