-- +goose Up
-- Whether the booking page shows already-booked times greyed out instead of hiding
-- them. Requested in discussion #14, tracked as issue #19.
--
-- Default 0, and that default is the point rather than caution. The slots endpoint is
-- public and unauthenticated, so turning this on makes the host's booked hours legible
-- to anyone with the link. That is a fair trade for a public-hours use case (an intro
-- call, a clinic, a tutor), where a visibly busy calendar communicates demand. It is a
-- privacy regression for an instance fronting a team's internal calendars, which is why
-- it must be chosen per event type and never inherited by surprise.
--
-- Only starts a booking or calendar conflict removed are shown. Times outside the
-- host's working hours are never rendered, so the shape of the working day is not
-- disclosed by the grid itself.
ALTER TABLE event_types ADD COLUMN show_taken_slots INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE event_types DROP COLUMN show_taken_slots;
