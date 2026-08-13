-- CleverRoute schema improvements: retention policies and housekeeping functions.
--
-- health_checks and usage tables grow over time. This migration adds:
--   1. prune_old_data() function to enforce retention limits:
--      - 7 days of raw health check history
--      - 90 days of request usage history
--   The gateway supervisor calls prune_old_data() periodically.
BEGIN;

CREATE OR REPLACE FUNCTION prune_old_data() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    -- Keep 7 days of health checks per router.
    DELETE FROM health_checks WHERE ts < now() - INTERVAL '7 days';

    -- Keep 90 days of usage data for quota & audit records.
    DELETE FROM usage WHERE ts < now() - INTERVAL '90 days';
END;
$$;

COMMIT;
