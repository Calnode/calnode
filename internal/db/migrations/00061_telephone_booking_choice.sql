-- +goose Up
ALTER TABLE event_types ADD COLUMN allow_phone_call INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bookings ADD COLUMN location_type TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE bookings DROP COLUMN location_type;
ALTER TABLE event_types DROP COLUMN allow_phone_call;
