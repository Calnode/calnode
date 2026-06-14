-- +goose Up

-- locked_until lets the worker reclaim jobs that were left in 'running'
-- state after a process crash. Set to now+N seconds on claim; the reaper
-- at the start of each Poll resets expired running jobs back to 'pending'.
ALTER TABLE jobs ADD COLUMN locked_until TEXT;

CREATE INDEX IF NOT EXISTS idx_jobs_running_expired
    ON jobs (locked_until) WHERE status = 'running';

-- +goose Down

DROP INDEX IF EXISTS idx_jobs_running_expired;
ALTER TABLE jobs DROP COLUMN locked_until;
