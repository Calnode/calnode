-- +goose Up
-- Workspace roles are Member / Admin / Owner (PRD §8.10). is_admin already
-- distinguishes member vs admin; is_owner adds the single-owner tier on top.
-- Owner implies admin. Exactly one owner exists at any time (enforced in app).
ALTER TABLE users ADD COLUMN is_owner INTEGER NOT NULL DEFAULT 0;

-- Backfill: the bootstrap user (earliest created) becomes the owner on upgrade.
-- Fresh installs set is_owner at Setup time instead; this no-ops when empty.
UPDATE users SET is_owner = 1, is_admin = 1
WHERE id = (SELECT id FROM users ORDER BY created_at ASC, id ASC LIMIT 1);

-- +goose Down
-- SQLite does not support DROP COLUMN here; this migration cannot be reversed.
