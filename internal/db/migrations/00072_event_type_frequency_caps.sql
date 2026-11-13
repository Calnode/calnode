-- +goose Up
-- Nullable per-event-type frequency caps evaluated in the host/event-type timezone.
ALTER TABLE event_types ADD COLUMN max_per_day INTEGER;
ALTER TABLE event_types ADD COLUMN max_per_week INTEGER;

-- +goose Down
-- Leave additive columns in place, consistent with other column migrations.
