-- +goose Up
-- Two job-table fixes:
-- 1. finished_at records when a job reached a terminal state, so completed rows
--    can be purged (nothing deleted done/failed jobs; the table grew for the life
--    of the SQLite file, which Litestream replicates).
-- 2. The (type, payload) uniqueness guard covered ALL statuses, so a done/failed
--    row permanently blocked re-enqueue of the same payload (e.g. retrying
--    reminders after a failure was a silent no-op). Scope it to live rows only.
ALTER TABLE jobs ADD COLUMN finished_at TEXT;

DROP INDEX IF EXISTS ux_jobs_type_payload;
CREATE UNIQUE INDEX IF NOT EXISTS ux_jobs_type_payload_live
    ON jobs (type, payload) WHERE status IN ('pending', 'running');

-- +goose Down
ALTER TABLE jobs DROP COLUMN finished_at;
DROP INDEX IF EXISTS ux_jobs_type_payload_live;
CREATE UNIQUE INDEX IF NOT EXISTS ux_jobs_type_payload
    ON jobs (type, payload);
