-- +goose Up
-- CalDAV calendar-home URL per connection. Discovery resolves principal ->
-- calendar-home -> collections once at connect time; the home URL is needed later
-- to re-list every collection (ListCalendars) without the server base URL, which
-- is not stored. Existing connections get NULL and keep the previous behaviour
-- (the single bound calendar) until reconnected.
ALTER TABLE calendar_connections ADD COLUMN caldav_home_url TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
