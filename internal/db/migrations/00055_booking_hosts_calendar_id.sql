-- +goose Up
-- Which calendar a booking's event was actually written into.
--
-- Until now only external_event_id was stored, and reschedule/cancel re-resolved the
-- target calendar from the host's CURRENT destination. That was harmless while the
-- destination was effectively fixed at the account's default calendar, but now that a host
-- can pick any calendar inside a connected account, changing that choice would leave every
-- existing booking's event id pointing at a calendar it does not live in: the update or
-- delete resolves to the new calendar, the provider returns 404, the booking cancels in
-- Calnode, and the meeting silently stays on the host's calendar forever.
--
-- Deliberately nullable with no backfill. Empty means "resolve the way we always did",
-- which is exactly right for rows created before this column existed - their events do live
-- in whatever the destination was and still is. Only new bookings record the calendar, so
-- the guarantee starts applying from here without rewriting history we cannot verify.
ALTER TABLE booking_hosts ADD COLUMN external_calendar_id TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
