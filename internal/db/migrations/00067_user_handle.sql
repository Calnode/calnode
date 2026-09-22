-- +goose Up
-- Public booking handle for per-person pages (/u/{handle}, #94). Nullable: only users
-- who set one get a page. Plain UNIQUE treats NULLs as distinct in SQLite, so any
-- number of handle-less users coexist; setting an in-use handle 409s at the API.
ALTER TABLE users ADD COLUMN handle TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_handle ON users (handle);

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
