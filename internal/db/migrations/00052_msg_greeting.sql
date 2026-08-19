-- +goose Up
-- Per-event-type override for the conversational assistant's opening greeting. Unlike
-- msg_confirmation/msg_cancellation/etc. (seeded with an English default at CreateEventType),
-- this is left NULL by default: the assistant falls back to the locale-keyed
-- "assistant_greeting" translation when unset, so translation keeps working automatically
-- for anyone who doesn't touch it. Only set (admin-authored, untranslated) once an operator
-- explicitly customizes it.
ALTER TABLE event_types ADD COLUMN msg_greeting TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before v3.35; leave the column in place.
