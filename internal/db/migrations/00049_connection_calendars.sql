-- +goose Up
-- Per-account calendar selection. A single connected account (calendar_connections,
-- keyed by user_id+provider+account_email) can now expose several calendars, each
-- independently included in free/busy conflict checks and optionally the write target.
--
-- Deliberately keyed by the STABLE account identity (user_id, provider, account_email),
-- NOT by calendar_connections.id: the connection row is deleted+reinserted (new id) on
-- every OAuth token refresh, so an FK to it would cascade-delete a user's calendar
-- selections on the next hourly refresh. Disconnect flows delete these rows explicitly.
CREATE TABLE IF NOT EXISTS connection_calendars (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider        TEXT NOT NULL,
    account_email   TEXT NOT NULL DEFAULT '',
    calendar_id     TEXT NOT NULL,          -- provider's calendar id ("primary", an address, a URL)
    name            TEXT NOT NULL DEFAULT '', -- display name (filled from the provider's calendar list)
    check_conflicts INTEGER NOT NULL DEFAULT 1, -- include this calendar in free/busy
    is_destination  INTEGER NOT NULL DEFAULT 0, -- write new booking events here (at most one per user)
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (user_id, provider, account_email, calendar_id)
);

CREATE INDEX IF NOT EXISTS idx_connection_calendars_account
    ON connection_calendars (user_id, provider, account_email);

-- Seed from existing connections so current behaviour is preserved on upgrade: each
-- connected account's single calendar becomes one selected calendar with the same flags.
INSERT OR IGNORE INTO connection_calendars
    (id, user_id, provider, account_email, calendar_id, name, check_conflicts, is_destination)
SELECT lower(hex(randomblob(16))), user_id, provider, COALESCE(account_email, ''),
       calendar_id, '', check_conflicts, is_destination
FROM calendar_connections;

-- +goose Down
DROP TABLE IF EXISTS connection_calendars;
