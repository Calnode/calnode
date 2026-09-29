-- +goose Up
-- Who sends the booker's calendar invite.
--
--   'calendar' (default, today's behaviour): the event is created on each host's connected
--   calendar with the booker as a guest, and Google/Microsoft email the invite. The invite
--   comes from the host's own account, so the booker sees the host's personal address.
--
--   'calnode': the host events are still created (they block time and mint the Meet/Teams
--   link) but WITHOUT the booker as a guest, so the provider emails nobody. Calnode sends
--   the invite itself as an .ics on the confirmation email, organized by the instance's
--   sender identity (Settings -> Email). For teams booking under one name, or hosts who
--   do not want their personal address on every invite.
--
-- Stored on the booking as well as the event type: the mode a booking was CREATED with
-- decides how its reschedule and cancel are delivered. Reading the event type's current
-- mode instead would, after the setting changes, cancel a Google-invited booking with an
-- .ics nobody has (or leave a Calnode-invited one with no cancellation at all).
ALTER TABLE event_types ADD COLUMN invite_delivery TEXT NOT NULL DEFAULT 'calendar'
    CHECK (invite_delivery IN ('calendar', 'calnode'));
ALTER TABLE bookings ADD COLUMN invite_delivery TEXT NOT NULL DEFAULT 'calendar'
    CHECK (invite_delivery IN ('calendar', 'calnode'));

-- +goose Down
ALTER TABLE bookings DROP COLUMN invite_delivery;
ALTER TABLE event_types DROP COLUMN invite_delivery;
