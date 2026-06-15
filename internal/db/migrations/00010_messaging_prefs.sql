-- +goose Up
-- User-level notification on/off toggles (all default 1 = on, preserving existing behaviour)
ALTER TABLE users ADD COLUMN notify_confirmation    INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_cancellation    INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_reschedule      INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_reminder        INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_host_booking    INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_host_cancel     INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_host_reschedule INTEGER NOT NULL DEFAULT 1;

-- Per-event-type custom notes appended to each email type
ALTER TABLE event_types ADD COLUMN msg_confirmation TEXT;
ALTER TABLE event_types ADD COLUMN msg_cancellation TEXT;
ALTER TABLE event_types ADD COLUMN msg_reschedule   TEXT;
ALTER TABLE event_types ADD COLUMN msg_reminder     TEXT;

-- Per-event-type reminder timing list (replaces the hardcoded 24h)
CREATE TABLE event_type_reminders (
    id            TEXT    PRIMARY KEY,
    event_type_id TEXT    NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
    hours_before  INTEGER NOT NULL CHECK(hours_before > 0),
    UNIQUE(event_type_id, hours_before)
);

-- +goose Down
-- SQLite does not support DROP COLUMN; migration is intentionally irreversible.
DROP TABLE IF EXISTS event_type_reminders;
