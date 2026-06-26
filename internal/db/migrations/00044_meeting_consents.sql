-- +goose Up
-- In-meeting recording consent — notice + consent-or-leave (Zoom/Teams/Meet model). This is an
-- AUDIT LOG of who acknowledged the recording notice; it does NOT gate recording (recording
-- starts on the host's click). One row per participant identity per room.
CREATE TABLE meeting_consents (
    room                 TEXT NOT NULL,
    participant_identity TEXT NOT NULL,
    name                 TEXT NOT NULL DEFAULT '',
    decision             TEXT NOT NULL DEFAULT 'continue',  -- continue | leave
    decided_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (room, participant_identity)
);

-- +goose Down
DROP TABLE IF EXISTS meeting_consents;
