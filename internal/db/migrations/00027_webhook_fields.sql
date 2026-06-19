-- +goose Up
-- Per-webhook payload field selection: a JSON array of field keys to include in
-- the delivery's "data" object. NULL means "the default set" — the original
-- booking-metadata payload — so existing webhooks keep their exact current shape
-- (and never start emitting attendee PII or answers without being reconfigured).
ALTER TABLE webhooks ADD COLUMN fields TEXT;

-- +goose Down
ALTER TABLE webhooks DROP COLUMN fields;
