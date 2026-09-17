-- +goose Up
-- One-time, short-lived password-reset tokens emailed to a user. We store only the
-- SHA-256 of the token (never the raw value); single-use is enforced by used_at.
-- Mirrors magic_link_tokens (00040) with a longer TTL: a reset email sits in an
-- inbox longer than a just-requested login link.
CREATE TABLE password_reset_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- +goose Down
DROP TABLE password_reset_tokens;
