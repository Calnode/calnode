-- +goose Up
-- Identify each connected calendar account so a user can connect several (e.g. work Google
-- + personal Gmail): re-auth of the same account upserts its row, a new account inserts a new
-- row. Used for dedup + display. Existing single connections backfill to '' and keep working
-- (free/busy doesn't need the email); they get a real value on next re-connect.
ALTER TABLE calendar_connections ADD COLUMN account_email TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
