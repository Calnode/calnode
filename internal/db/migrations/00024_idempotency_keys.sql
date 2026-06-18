-- +goose Up
-- Idempotency keys for POST /v1/bookings. A client (e.g. an automation agent)
-- can safely retry a booking with the same Idempotency-Key header: the original
-- response is replayed verbatim instead of creating a duplicate booking. The key
-- is reserved (status_code NULL) while the original request runs; on success the
-- response is stored, on failure the row is released so a retry can proceed.
-- Rows are purged by the worker 24h after creation.
CREATE TABLE idempotency_keys (
    idempotency_key TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    status_code     INTEGER,        -- NULL while the original request is in flight
    response_body   TEXT,
    booking_id      TEXT,
    created_at      TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
