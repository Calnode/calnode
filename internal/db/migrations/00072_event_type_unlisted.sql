-- +goose Up
-- is_public remains the direct-booking gate. Existing rows keep their current
-- visibility; only an explicit choice of Unlisted hides a bookable event type.
ALTER TABLE event_types ADD COLUMN is_unlisted INTEGER NOT NULL DEFAULT 0 CHECK (is_unlisted IN (0, 1));

-- +goose Down
-- An older binary cannot hide a bookable event type from its directory. Make
-- unlisted types private rather than unexpectedly publishing them on rollback.
UPDATE event_types SET is_public = 0 WHERE is_unlisted = 1;
ALTER TABLE event_types DROP COLUMN is_unlisted;
