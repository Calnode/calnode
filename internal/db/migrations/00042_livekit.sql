-- +goose NO TRANSACTION
-- LiveKit (self-hostable WebRTC video) as a built-in meeting location.
--
-- Two parts:
--   1. Instance-level config columns (server URL + API key/secret, secret encrypted) on
--      server_settings, plus a livekit_room column on bookings — like Zoom/Stripe.
--   2. Widen the event_types.location_type CHECK to allow 'livekit'. SQLite can't ALTER a
--      CHECK, so this is the standard table rebuild. It runs with NO TRANSACTION +
--      foreign_keys=OFF because a DROP TABLE with FKs enabled would cascade-delete the
--      child bookings/hosts; toggling FKs off keeps the copied data intact (PRAGMA
--      foreign_keys is a no-op inside a transaction, hence NO TRANSACTION).

-- +goose Up
ALTER TABLE server_settings ADD COLUMN livekit_url            TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN livekit_api_key        TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN livekit_api_secret_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN livekit_room TEXT NOT NULL DEFAULT '';

PRAGMA foreign_keys=OFF;

CREATE TABLE event_types_new (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id               TEXT REFERENCES teams(id) ON DELETE SET NULL,
    slug                  TEXT NOT NULL UNIQUE,
    name                  TEXT NOT NULL,
    description           TEXT,
    duration_minutes      INTEGER NOT NULL,
    slot_interval_minutes INTEGER NOT NULL DEFAULT 30,
    location_type         TEXT NOT NULL DEFAULT 'link'
                            CHECK (location_type IN ('zoom','google_meet','teams','custom_video','phone','in_person','link','livekit')),
    location_value        TEXT,
    routing_mode          TEXT NOT NULL DEFAULT 'fixed'
                            CHECK (routing_mode IN ('fixed', 'round_robin', 'collective', 'priority')),
    buffer_before_minutes INTEGER NOT NULL DEFAULT 0,
    buffer_after_minutes  INTEGER NOT NULL DEFAULT 0,
    min_notice_minutes    INTEGER NOT NULL DEFAULT 0,
    max_future_days       INTEGER NOT NULL DEFAULT 60,
    seat_limit            INTEGER NOT NULL DEFAULT 1,
    is_active             INTEGER NOT NULL DEFAULT 1,
    is_public             INTEGER NOT NULL DEFAULT 1,
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    msg_confirmation      TEXT,
    msg_cancellation      TEXT,
    msg_reschedule        TEXT,
    msg_reminder          TEXT,
    max_active_bookings   INTEGER NOT NULL DEFAULT 1,
    rr_strategy           TEXT NOT NULL DEFAULT 'even'
                            CHECK (rr_strategy IN ('even', 'soonest', 'priority')),
    subj_confirmation     TEXT,
    subj_cancellation     TEXT,
    subj_reschedule       TEXT,
    subj_reminder         TEXT,
    price_cents           INTEGER NOT NULL DEFAULT 0,
    currency              TEXT    NOT NULL DEFAULT 'usd'
);

INSERT INTO event_types_new SELECT * FROM event_types;
DROP TABLE event_types;
ALTER TABLE event_types_new RENAME TO event_types;

PRAGMA foreign_keys=ON;

-- +goose Down
-- Irreversible widening of a CHECK; the added columns are harmless. No-op down.
