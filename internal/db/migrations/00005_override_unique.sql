-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_availability_overrides_user_date
    ON availability_overrides (user_id, date);

-- +goose Down
DROP INDEX IF EXISTS idx_availability_overrides_user_date;
