-- +goose Up
-- Provider-neutral payment columns (#246 step 1).
--
-- Stripe was the only implementation, so every existing row is Stripe: payment_provider
-- defaults to 'stripe' and step 3 will start writing it explicitly once a second
-- provider exists. The old names stay in the migration history; renaming keeps the data.
ALTER TABLE bookings RENAME COLUMN stripe_session_id TO payment_session_id;
ALTER TABLE bookings RENAME COLUMN stripe_payment_intent_id TO payment_ref;
ALTER TABLE bookings ADD COLUMN payment_provider TEXT NOT NULL DEFAULT 'stripe';

-- +goose Down
ALTER TABLE bookings DROP COLUMN payment_provider;
ALTER TABLE bookings RENAME COLUMN payment_ref TO stripe_payment_intent_id;
ALTER TABLE bookings RENAME COLUMN payment_session_id TO stripe_session_id;
