-- +goose Up
-- Captures the attendee's resolved page locale at booking time (mirrors iana_timezone) —
-- this can only be captured now, not reconstructed later, since it needs the actual
-- Accept-Language/cookie/lang= state the visitor saw when they booked. Not yet consumed by
-- emails (mailer has no i18n support yet — see internal-docs/i18n-plan.md); this just
-- captures the data so it exists once that work lands. Existing rows backfill to 'en'.
ALTER TABLE booking_attendees ADD COLUMN locale TEXT NOT NULL DEFAULT 'en';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
