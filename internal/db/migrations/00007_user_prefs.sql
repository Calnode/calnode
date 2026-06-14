-- +goose Up
ALTER TABLE users ADD COLUMN time_format TEXT NOT NULL DEFAULT '12h';
ALTER TABLE users ADD COLUMN week_start  INTEGER NOT NULL DEFAULT 1; -- 1=Monday, 0=Sunday

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; migration is intentionally irreversible.
