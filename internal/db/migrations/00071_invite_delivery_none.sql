-- +goose Up
-- A third invite_delivery mode, 'none': the hosts' events are created without guests,
-- exactly as for 'calnode', but Calnode attaches no invite to the booker's emails either.
-- For integrations that deliver the invite themselves (their own confirmation email with
-- their own .ics), where any invite from the provider or from Calnode would be a duplicate.
--
-- 00068 declared the allowed values as a column CHECK, and SQLite cannot ALTER a CHECK.
-- Rather than rebuild bookings and event_types (00042 rebuilt event_types with
-- NO TRANSACTION + foreign_keys=OFF; bookings has never been rebuilt, see 00037, and
-- carries the double-booking unique index and five child tables' foreign keys), the
-- column is swapped: add a widened copy, fill it, drop the old column, take its name.
-- DROP COLUMN is allowed because nothing else in the schema names invite_delivery (its
-- only CHECK is its own), so this runs in goose's normal transaction with foreign keys
-- on, and neither table's indexes or child rows are touched. The column moves to the end
-- of each table; every query names its columns.
ALTER TABLE event_types ADD COLUMN invite_delivery_new TEXT NOT NULL DEFAULT 'calendar'
    CHECK (invite_delivery_new IN ('calendar', 'calnode', 'none'));
UPDATE event_types SET invite_delivery_new = invite_delivery;
ALTER TABLE event_types DROP COLUMN invite_delivery;
ALTER TABLE event_types RENAME COLUMN invite_delivery_new TO invite_delivery;

ALTER TABLE bookings ADD COLUMN invite_delivery_new TEXT NOT NULL DEFAULT 'calendar'
    CHECK (invite_delivery_new IN ('calendar', 'calnode', 'none'));
UPDATE bookings SET invite_delivery_new = invite_delivery;
ALTER TABLE bookings DROP COLUMN invite_delivery;
ALTER TABLE bookings RENAME COLUMN invite_delivery_new TO invite_delivery;

-- +goose Down
-- The widened CHECK stays (as in 00042). 'none' rows become 'calnode', the mode an older
-- binary understands that also keeps the hosts' events guestless: read as 'calendar', the
-- next reschedule or reconcile would have the host's calendar invite the booker after all.
UPDATE bookings SET invite_delivery = 'calnode' WHERE invite_delivery = 'none';
UPDATE event_types SET invite_delivery = 'calnode' WHERE invite_delivery = 'none';
