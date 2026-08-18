-- +goose Up
-- Banner (instance-wide, on the singleton row): an optional full-width image
-- shown below the logo on the public booking/manage pages and in emails.
--   banner_url     absolute https URL to a banner image; empty = hidden.
--   banner_opacity 20-100; CSS opacity. 100 = fully opaque.
ALTER TABLE server_settings ADD COLUMN banner_url TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN banner_opacity INTEGER NOT NULL DEFAULT 100;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN banner_url;
ALTER TABLE server_settings DROP COLUMN banner_opacity;
