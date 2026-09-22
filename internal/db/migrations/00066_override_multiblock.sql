-- +goose Up
-- Multi-block custom-hours overrides (#95): a date may hold several custom-hours
-- blocks (e.g. 09:00-12:00 + 13:00-17:00 around a mid-day appointment) instead of one.
-- The old single-row uniqueness becomes two partial indexes: at most one blocking
-- (day_off/out_of_office) row per date, and no exact-duplicate custom blocks.
-- resolveDay gives a blocking row priority over customs on the same date.
DROP INDEX IF EXISTS idx_availability_overrides_user_date;
CREATE UNIQUE INDEX IF NOT EXISTS idx_availability_overrides_blocked
    ON availability_overrides (user_id, date) WHERE is_available = 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_availability_overrides_custom
    ON availability_overrides (user_id, date, start_time, end_time) WHERE is_available = 1;

-- +goose Down
-- Intentionally irreversible: dates with several custom blocks cannot satisfy the
-- old single-row uniqueness, so downgrading with such data would fail or silently
-- drop blocks. SQLite doesn't support DROP COLUMN in older versions either.
