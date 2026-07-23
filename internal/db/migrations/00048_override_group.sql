-- +goose Up
-- group_id ties together the per-date rows created from a single date-range block
-- (an "out of office" span), so the UI can show and delete them as one entry. NULL
-- for single-date overrides. Slot generation is unchanged — it still reads the
-- individual per-date rows; the group is purely an admin-side convenience.
ALTER TABLE availability_overrides ADD COLUMN group_id TEXT;

-- +goose Down
ALTER TABLE availability_overrides DROP COLUMN group_id;
