-- +goose Up
-- Track who archived a member so restore can be gated: the owner can restore
-- anyone; an admin can restore only members they archived themselves.
ALTER TABLE users ADD COLUMN archived_by TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN here; this migration cannot be reversed.
