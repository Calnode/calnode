-- +goose Up
-- Logo opacity as a percentage (20–100); lets operators make the logo subtle.
-- Applied as CSS opacity in emails + public pages. Default 100 = fully opaque.
ALTER TABLE server_settings ADD COLUMN logo_opacity INTEGER NOT NULL DEFAULT 100;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN logo_opacity;
