-- +goose NO TRANSACTION
-- 'phone_outbound' location type: the host calls the attendee. The booking form asks
-- the attendee for a required phone number, and the booking is stored exactly like the
-- allow_phone_call path (bookings.location_type = 'phone', location_value = 'tel:<number>'),
-- so tap-to-call, the calendar event location and the emails work unchanged. Only the
-- event type carries the new value; bookings.location_type has no CHECK.
--
-- Widening the location_type CHECK is the 00042 table rebuild: NO TRANSACTION +
-- foreign_keys=OFF, because a DROP TABLE with foreign keys enabled would cascade-delete
-- the child rows (bookings, hosts, questions, reminders, availability rules), and PRAGMA
-- foreign_keys is a no-op inside a transaction. Columns are named explicitly, in the
-- table's current order (the ones added by ALTER after 00042 sit at the end).

-- +goose Up
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
                            CHECK (location_type IN ('zoom','google_meet','teams','custom_video','phone','in_person','link','livekit','phone_outbound')),
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
    currency              TEXT    NOT NULL DEFAULT 'usd',
    archived_at           TEXT,
    msg_greeting          TEXT,
    show_taken_slots      INTEGER NOT NULL DEFAULT 0,
    allow_phone_call      INTEGER NOT NULL DEFAULT 0,
    display_order         INTEGER NOT NULL DEFAULT 0,
    invite_delivery       TEXT NOT NULL DEFAULT 'calendar'
                            CHECK (invite_delivery IN ('calendar', 'calnode', 'none'))
);

INSERT INTO event_types_new (
    id, user_id, team_id, slug, name, description, duration_minutes, slot_interval_minutes,
    location_type, location_value, routing_mode, buffer_before_minutes, buffer_after_minutes,
    min_notice_minutes, max_future_days, seat_limit, is_active, is_public, created_at,
    msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, max_active_bookings,
    rr_strategy, subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder,
    price_cents, currency, archived_at, msg_greeting, show_taken_slots, allow_phone_call,
    display_order, invite_delivery)
SELECT
    id, user_id, team_id, slug, name, description, duration_minutes, slot_interval_minutes,
    location_type, location_value, routing_mode, buffer_before_minutes, buffer_after_minutes,
    min_notice_minutes, max_future_days, seat_limit, is_active, is_public, created_at,
    msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, max_active_bookings,
    rr_strategy, subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder,
    price_cents, currency, archived_at, msg_greeting, show_taken_slots, allow_phone_call,
    display_order, invite_delivery
FROM event_types;

DROP TABLE event_types;
ALTER TABLE event_types_new RENAME TO event_types;

PRAGMA foreign_keys=ON;

-- +goose Down
-- The widened CHECK stays (as in 00042 and 00071). An older binary does not know
-- 'phone_outbound', so those event types become 'phone' with no host number and are
-- deactivated: still a phone call, never silently a video meeting, and not bookable
-- until the operator enters a number (validateLocation requires one for 'phone').
-- Existing bookings are unaffected: they were stored as 'phone' + 'tel:<number>'.
UPDATE event_types SET location_type = 'phone', location_value = NULL, is_active = 0
WHERE location_type = 'phone_outbound';
