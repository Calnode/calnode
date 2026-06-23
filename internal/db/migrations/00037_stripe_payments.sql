-- +goose Up
-- Stripe API credentials (one Stripe account per instance; admin configures in Settings).
ALTER TABLE server_settings ADD COLUMN stripe_secret_key_enc     TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN stripe_publishable_key    TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN stripe_webhook_secret_enc TEXT NOT NULL DEFAULT '';

-- Per-event-type price. 0 = free (the default; today's flow is unchanged).
ALTER TABLE event_types ADD COLUMN price_cents INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN currency    TEXT    NOT NULL DEFAULT 'usd';

-- Payment state, separate from booking status so a paid hold can occupy the slot
-- (status='confirmed', so the double-booking guard reserves it) while still awaiting
-- payment (payment_status='pending'); side-effects are deferred until 'paid'. A new
-- booking-status value would have required a SQLite table rebuild (CHECK constraint).
--   none      → free booking, no payment involved (default)
--   pending   → awaiting Stripe Checkout completion (slot held)
--   paid      → payment captured; confirmation side-effects have run
--   refunded  → payment refunded (on cancel)
ALTER TABLE bookings ADD COLUMN payment_status           TEXT NOT NULL DEFAULT 'none';
ALTER TABLE bookings ADD COLUMN stripe_session_id        TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN stripe_payment_intent_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave columns in place.
