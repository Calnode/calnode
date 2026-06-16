-- +goose Up
-- Cap how many active (upcoming, non-cancelled) bookings a single invitee may
-- hold for an event type, keyed by their email. 1 = one at a time (default);
-- 0 = unlimited. Existing rows adopt the default of 1.
ALTER TABLE event_types ADD COLUMN max_active_bookings INTEGER NOT NULL DEFAULT 1;

-- +goose Down
-- SQLite does not support DROP COLUMN; this migration cannot be reversed.
