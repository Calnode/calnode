-- +goose Up
-- Record what was actually charged on the booking (immutable payment record), independent
-- of the event type's current price_cents which can change later. Set from the Stripe
-- Checkout session's amount_total/currency at confirmation. 0/'' for free bookings.
ALTER TABLE bookings ADD COLUMN amount_paid_cents    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bookings ADD COLUMN amount_paid_currency TEXT    NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave columns in place.
