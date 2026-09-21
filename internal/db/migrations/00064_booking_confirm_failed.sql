-- +goose Up
-- Operator-visible flag for a lost initial confirmation: dispatchBookingConfirmation
-- runs in the background, and a failed attendee/host send used to be log-only.
-- confirm_failed = 1 means at least one confirmation send failed (after retry);
-- surfaced on the booking JSON so the admin UI can badge it.
ALTER TABLE bookings ADD COLUMN confirm_failed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE bookings DROP COLUMN confirm_failed;
