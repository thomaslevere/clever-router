-- Migration 0008: Add desired_status column for persistent desired state reconciliation
ALTER TABLE routers 
ADD COLUMN IF NOT EXISTS desired_status VARCHAR(20) DEFAULT 'running';

-- Backfill desired_status from desired_state
UPDATE routers SET desired_status = desired_state WHERE desired_status IS NULL OR desired_status = '';
UPDATE routers SET desired_status = 'running' WHERE desired_status IS NULL OR desired_status = '';
