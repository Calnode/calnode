-- +goose Up
-- Member offboarding is archiving (soft-delete), not hard delete: the row and
-- all its links (past bookings, event types, team memberships) are preserved.
-- archived_at NULL = active; a timestamp = archived (login blocked, hidden from
-- default lists, skipped in routing).
ALTER TABLE users ADD COLUMN archived_at TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN here; this migration cannot be reversed.
