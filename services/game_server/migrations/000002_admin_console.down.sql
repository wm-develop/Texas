DROP TABLE IF EXISTS server_settings;
DROP INDEX IF EXISTS users_role_status_idx;
ALTER TABLE users DROP COLUMN IF EXISTS role;
