-- +goose Up
-- Native GA4 / GTM: store just the ID; the booking page renders the official loader snippet
-- (no need to paste the whole script). Empty = that tag is off. Validated to the ID format on
-- write so the value is safe to interpolate into a script.
ALTER TABLE server_settings ADD COLUMN gtm_container_id  TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN ga4_measurement_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the columns in place.
