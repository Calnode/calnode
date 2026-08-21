-- +goose Up
-- An optional Resend API key, so Calnode can deliver over Resend's HTTPS API instead of
-- SMTP. Needed because several hosting platforms (Railway below Pro, among others) block
-- outbound SMTP on their cheaper plans by dropping the packets, which is indistinguishable
-- from a misconfiguration and cannot be worked around at the SMTP layer at all.
--
-- Encrypted at rest with the same envelope scheme as smtp_pass_enc; never returned by the
-- API, which exposes only a resend_api_key_set boolean.
--
-- Presence of this key is what selects the transport: set means use the HTTPS API, empty
-- means fall back to SMTP. That is deliberate over probing SMTP at startup and switching
-- automatically - a probe tests reachability at boot, not at send time, and a working TCP
-- connection is not the same thing as a working delivery path. See internal/mailer/resend.go.
ALTER TABLE server_settings ADD COLUMN resend_api_key_enc TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
