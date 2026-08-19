-- +goose Up
-- What a visitor sees when their browser doesn't ask for any locale Calnode supports
-- (default English) — e.g. an operator serving a mostly Spanish-speaking customer base
-- might want Spanish instead. See internal/i18n.ResolveWithFallback.
ALTER TABLE server_settings ADD COLUMN fallback_locale TEXT NOT NULL DEFAULT 'en';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
