-- +goose Up
-- Public directory order is independent of names and shared booking URLs.
-- Equal values retain alphabetical ordering; existing event types all start at 0.
ALTER TABLE event_types ADD COLUMN display_order INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- Leave the additive column in place, consistent with other column migrations.
