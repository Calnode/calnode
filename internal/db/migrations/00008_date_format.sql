-- +goose Up
ALTER TABLE users ADD COLUMN date_format TEXT NOT NULL DEFAULT 'dmy';
-- Guarantee no NULLs survive on existing rows (SQLite ALTER TABLE default handling varies).
UPDATE users SET date_format = 'dmy' WHERE date_format IS NULL;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; migration is intentionally irreversible.
