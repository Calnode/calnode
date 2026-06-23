-- +goose Up
-- One-time, short-lived login links emailed to a user. We store only the SHA-256 of the
-- token (never the raw value); single-use is enforced by used_at.
CREATE TABLE magic_link_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- +goose Down
DROP TABLE magic_link_tokens;
