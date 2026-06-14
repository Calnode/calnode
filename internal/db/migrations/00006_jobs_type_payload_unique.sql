-- +goose Up

-- Prevent duplicate jobs of the same type+payload (e.g. two reminder.send jobs
-- for the same booking_id if enqueueReminder is called more than once).
CREATE UNIQUE INDEX IF NOT EXISTS ux_jobs_type_payload
    ON jobs (type, payload);

-- +goose Down
DROP INDEX IF EXISTS ux_jobs_type_payload;
