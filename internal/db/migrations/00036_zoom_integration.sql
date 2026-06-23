-- +goose Up
-- Zoom OAuth app credentials (one app per instance; each host then connects their
-- own Zoom account via OAuth).
ALTER TABLE server_settings ADD COLUMN zoom_client_id         TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN zoom_client_secret_enc TEXT NOT NULL DEFAULT '';

-- Per-host Zoom OAuth tokens. Zoom is a meeting-link provider, not a calendar, so it gets
-- its own table (calendar_connections.provider has a CHECK constraint for calendar kinds).
-- One connection per user (user_id PK).
CREATE TABLE zoom_connections (
    user_id           TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token_enc  TEXT NOT NULL,
    refresh_token_enc TEXT NOT NULL DEFAULT '',
    expiry_at         TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

-- The Zoom meeting id minted for a booking (used to update/delete the meeting on
-- reschedule/cancel). Empty for non-Zoom bookings or manual links.
ALTER TABLE bookings ADD COLUMN zoom_meeting_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave columns in place.
