-- Migration 0007: Add provider_type and route_path columns to routers table
ALTER TABLE routers 
ADD COLUMN IF NOT EXISTS provider_type TEXT DEFAULT 'omniroute',
ADD COLUMN IF NOT EXISTS route_path TEXT DEFAULT '';

UPDATE routers SET provider_type = adapter_type WHERE provider_type IS NULL OR provider_type = '';
UPDATE routers SET route_path = endpoint_path WHERE route_path IS NULL OR route_path = '';

CREATE INDEX IF NOT EXISTS idx_routers_route_path ON routers(route_path);
