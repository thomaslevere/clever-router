-- Fix native_panel_url that was stored as internal docker IP
UPDATE routers 
SET native_panel_url = CASE 
    WHEN endpoint_path != '' THEN endpoint_path || '/dashboard'
    ELSE '/' || slug || '/dashboard'
END
WHERE native_panel_url LIKE 'http://%' OR native_panel_url LIKE 'https://%';
