-- +goose Up
-- Legal links (instance-wide, on the singleton settings row): absolute URLs to the
-- operator's own Privacy Policy and Terms. Shown as links in the public booking-page
-- footer and linked from the cookie-consent banner. Empty = the link is hidden.
-- The operator is the data controller; Calnode only surfaces the links they provide.
ALTER TABLE server_settings ADD COLUMN privacy_url TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN terms_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE server_settings DROP COLUMN privacy_url;
ALTER TABLE server_settings DROP COLUMN terms_url;
