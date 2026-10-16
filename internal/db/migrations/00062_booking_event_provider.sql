-- +goose Up
-- Stamp which provider wrote a booking's calendar event, so reschedule and
-- cancel act on the account that holds the event even after the host moves
-- their destination to another provider (issue #58). Empty means "recorded
-- before stamping": fall back to event-id recognition (CalDAV URLs) and then
-- the current destination, the previous behaviour.
ALTER TABLE booking_hosts ADD COLUMN external_provider TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE booking_hosts DROP COLUMN external_provider;
