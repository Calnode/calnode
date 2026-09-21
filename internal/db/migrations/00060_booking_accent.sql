-- +goose Up
ALTER TABLE users ADD COLUMN booking_accent TEXT NOT NULL DEFAULT '#111827';

-- +goose Down
ALTER TABLE users DROP COLUMN booking_accent;
