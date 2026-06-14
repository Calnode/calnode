-- +goose Up
CREATE TABLE IF NOT EXISTS booking_manage_tokens (
    token_hash  TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_manage_tokens_booking
    ON booking_manage_tokens (booking_id);

-- +goose Down
DROP INDEX IF EXISTS idx_manage_tokens_booking;
DROP TABLE IF EXISTS booking_manage_tokens;
