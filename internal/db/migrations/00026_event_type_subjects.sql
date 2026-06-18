-- +goose Up
-- Per-event-type custom email subjects for the attendee-facing emails. NULL/empty
-- means "use the built-in subject" — so existing rows and new ones default to the
-- current behaviour. Mirrors the msg_* custom-note columns.
ALTER TABLE event_types ADD COLUMN subj_confirmation TEXT;
ALTER TABLE event_types ADD COLUMN subj_cancellation TEXT;
ALTER TABLE event_types ADD COLUMN subj_reschedule   TEXT;
ALTER TABLE event_types ADD COLUMN subj_reminder     TEXT;

-- +goose Down
ALTER TABLE event_types DROP COLUMN subj_confirmation;
ALTER TABLE event_types DROP COLUMN subj_cancellation;
ALTER TABLE event_types DROP COLUMN subj_reschedule;
ALTER TABLE event_types DROP COLUMN subj_reminder;
