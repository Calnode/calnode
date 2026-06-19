-- +goose Up
-- Logo display height in px (email header); public pages scale it up modestly.
-- Operator-adjustable via a 16–64px slider in Settings → Branding; 28 = sensible default.
ALTER TABLE server_settings ADD COLUMN logo_height INTEGER NOT NULL DEFAULT 28;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN logo_height;
