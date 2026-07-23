-- +goose Up
-- Archived event types: soft-hide from the default admin list without deleting the
-- row (deletion is blocked by ON DELETE RESTRICT once any booking exists). NULL =
-- not archived. Archiving also sets is_active = 0, so every existing bookability gate
-- (public booking page, the shared booking-creation core, MCP) already excludes an
-- archived type — no query needs to learn about archived_at to stay correct. Reversible.
ALTER TABLE event_types ADD COLUMN archived_at TEXT;

-- +goose Down
ALTER TABLE event_types DROP COLUMN archived_at;
