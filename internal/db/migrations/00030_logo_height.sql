-- +goose Up
-- Logo display height in px (email header). Public pages scale it up modestly.
-- Presets in the UI map to 22 (small) / 30 (medium) / 40 (large); default small.
ALTER TABLE server_settings ADD COLUMN logo_height INTEGER NOT NULL DEFAULT 22;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN logo_height;
