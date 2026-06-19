-- +goose Up
-- Branding (instance-wide, on the singleton row):
--   business_name  display name used as the wordmark in emails and on the public
--                  booking/manage pages. Falls back to "Calnode" when empty.
--   logo_url       absolute https URL to a logo image, shown in the email header
--                  and the public page header. Empty = text wordmark only.
ALTER TABLE server_settings ADD COLUMN business_name TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN logo_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE server_settings DROP COLUMN business_name;
ALTER TABLE server_settings DROP COLUMN logo_url;
