-- +goose Up
-- Self-service password reset links (issue #34). Same shape as magic_link_tokens
-- (00040): only the SHA-256 of the token is stored, never the raw value, so a copy of
-- the database (or of its Litestream replica) cannot be turned into a working link.
-- Single use is enforced by a conditional UPDATE on used_at, not by this schema.
--
-- created_at is written by the application in RFC 3339 rather than defaulted by
-- SQLite, because it is compared against RFC 3339 cutoffs: the per-account cooldown
-- between reset emails reads it, and datetime('now') produces a different text format
-- that would not order correctly against them.
CREATE TABLE password_reset_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens(user_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_password_reset_tokens_user;
DROP TABLE IF EXISTS password_reset_tokens;
