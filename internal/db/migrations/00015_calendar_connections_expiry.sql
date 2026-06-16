-- +goose Up
ALTER TABLE calendar_connections ADD COLUMN expiry_at TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN; this migration cannot be reversed.
