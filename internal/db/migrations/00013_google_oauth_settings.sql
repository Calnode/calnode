-- +goose Up
ALTER TABLE server_settings ADD COLUMN google_client_id         TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN google_client_secret_enc TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave columns in place.
