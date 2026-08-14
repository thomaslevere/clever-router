-- Migration: 0006_add_router_env_vars.sql
-- Adds support for encrypted environment variables and automatic restart preferences on routers.
BEGIN;

ALTER TABLE routers ADD COLUMN IF NOT EXISTS env_vars TEXT DEFAULT '[]';
ALTER TABLE routers ADD COLUMN IF NOT EXISTS auto_restart_on_env_change BOOLEAN DEFAULT FALSE;

COMMIT;
