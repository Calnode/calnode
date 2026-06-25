-- +goose Up
-- Meeting recording (LiveKit Egress). Off unless enabled; recordings upload to the same
-- S3 bucket Litestream backs up to (LITESTREAM_* env), under a recordings/ prefix.
ALTER TABLE server_settings ADD COLUMN recordings_enabled INTEGER NOT NULL DEFAULT 0;

CREATE TABLE recordings (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT,                                   -- derived from room "booking-<id>"; nullable
    room        TEXT NOT NULL,
    egress_id   TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active',         -- active | complete | failed
    object_key  TEXT NOT NULL DEFAULT '',               -- S3 key of the finished file
    duration_s  INTEGER NOT NULL DEFAULT 0,
    started_by  TEXT NOT NULL DEFAULT '',               -- host participant identity
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX idx_recordings_room ON recordings(room);
CREATE INDEX idx_recordings_egress ON recordings(egress_id);

-- +goose Down
-- Leave the column; drop the table.
DROP TABLE IF EXISTS recordings;
