-- +goose Up
-- Notetaker: transcribe finished recordings (Deepgram) and summarise them (the BYO-LLM layer)
-- into notes attached to the booking. Off unless enabled + a Deepgram key is set.
ALTER TABLE server_settings ADD COLUMN notetaker_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_settings ADD COLUMN stt_api_key_enc   TEXT NOT NULL DEFAULT '';  -- Deepgram key (encrypted)

CREATE TABLE transcripts (
    id           TEXT PRIMARY KEY,
    booking_id   TEXT,                                   -- nullable; grouping key
    recording_id TEXT NOT NULL,                          -- one transcript per recording
    room         TEXT NOT NULL,
    text         TEXT NOT NULL DEFAULT '',
    segments     TEXT NOT NULL DEFAULT '[]',             -- JSON [{speaker,start,end,text}]
    status       TEXT NOT NULL DEFAULT 'complete',       -- complete | failed
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX idx_transcripts_booking ON transcripts(booking_id);
CREATE INDEX idx_transcripts_recording ON transcripts(recording_id);

CREATE TABLE notes (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL,                           -- one notes doc per booking (regenerable)
    content     TEXT NOT NULL DEFAULT '',                -- markdown summary
    model       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'complete',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX idx_notes_booking ON notes(booking_id);

-- +goose Down
DROP TABLE IF EXISTS notes;
DROP TABLE IF EXISTS transcripts;
