-- +goose Up
CREATE TABLE IF NOT EXISTS single_use_tokens (
    id                 TEXT PRIMARY KEY,
    token              TEXT NOT NULL UNIQUE,
    event_type_id      TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at         DATETIME,
    used_at            DATETIME,
    booking_id         TEXT,
    created_at         DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_single_use_tokens_event_created
    ON single_use_tokens(event_type_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_single_use_tokens_event_created;
DROP TABLE IF EXISTS single_use_tokens;
