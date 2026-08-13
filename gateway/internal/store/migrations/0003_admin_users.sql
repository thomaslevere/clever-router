-- Pre-seed admin user accounts for CleverRoute control plane.
-- User 1: username: salman, pass: 136517 (role: owner)
-- User 2: username: azam, pass: 136517 (role: admin)
BEGIN;

-- Add username column to users table if it doesn't exist.
ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT UNIQUE;

-- Create index on lowercase username for fast case-insensitive login lookups.
CREATE INDEX IF NOT EXISTS idx_users_username_lower ON users (LOWER(username));

-- Pre-seed 'salman' with bcrypt hash of '136517'
INSERT INTO users (username, email, password_hash, role)
VALUES ('salman', 'salman@cleverroute.local', '$2b$10$REft/dTWiwDrtcRt5z6t.uhpePANiuGuIhyvhjlB2m4TUUgzCHfIa', 'owner')
ON CONFLICT (username) DO UPDATE
SET password_hash = EXCLUDED.password_hash, role = 'owner';

-- Pre-seed 'azam' with bcrypt hash of '136517'
INSERT INTO users (username, email, password_hash, role)
VALUES ('azam', 'azam@cleverroute.local', '$2b$10$REft/dTWiwDrtcRt5z6t.uhpePANiuGuIhyvhjlB2m4TUUgzCHfIa', 'admin')
ON CONFLICT (username) DO UPDATE
SET password_hash = EXCLUDED.password_hash, role = 'admin';

COMMIT;
