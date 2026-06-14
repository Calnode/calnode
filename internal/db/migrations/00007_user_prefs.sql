-- +goose Up
ALTER TABLE users ADD COLUMN time_format TEXT NOT NULL DEFAULT '12h';
ALTER TABLE users ADD COLUMN week_start  INTEGER NOT NULL DEFAULT 1; -- 1=Monday, 0=Sunday
-- Guarantee no NULLs survive on existing rows (SQLite ALTER TABLE default handling varies).
UPDATE users SET time_format = '12h' WHERE time_format IS NULL;
UPDATE users SET week_start  = 1     WHERE week_start  IS NULL;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; migration is intentionally irreversible.
