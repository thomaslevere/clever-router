-- Migration 0009: Backfill all existing routers to desired_state = 'running' so boot supervisor auto-starts them
UPDATE routers SET desired_state = 'running', desired_status = 'running' WHERE desired_state IS NULL OR desired_state = '' OR desired_state = 'stopped';
