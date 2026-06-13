-- +goose Up

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    iana_timezone TEXT NOT NULL DEFAULT 'UTC',
    avatar_url    TEXT,
    is_admin      INTEGER NOT NULL DEFAULT 0,  -- first user bootstraps as admin
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,  -- stored hashed; shown once on creation
    last_used_at TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS teams (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS team_members (
    id               TEXT PRIMARY KEY,
    team_id          TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role             TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
    routing_priority INTEGER NOT NULL DEFAULT 0,
    UNIQUE (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS event_types (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id               TEXT REFERENCES teams(id) ON DELETE SET NULL,
    slug                  TEXT NOT NULL UNIQUE,  -- unique within workspace (§5)
    name                  TEXT NOT NULL,
    description           TEXT,
    duration_minutes      INTEGER NOT NULL,
    slot_interval_minutes INTEGER NOT NULL DEFAULT 30,
    location_type         TEXT NOT NULL DEFAULT 'link'
                            CHECK (location_type IN ('zoom','google_meet','teams','custom_video','phone','in_person','link')),
    location_value        TEXT,
    routing_mode          TEXT NOT NULL DEFAULT 'fixed'
                            CHECK (routing_mode IN ('fixed', 'round_robin', 'collective', 'priority')),
    buffer_before_minutes INTEGER NOT NULL DEFAULT 0,
    buffer_after_minutes  INTEGER NOT NULL DEFAULT 0,
    min_notice_minutes    INTEGER NOT NULL DEFAULT 0,
    max_future_days       INTEGER NOT NULL DEFAULT 60,
    seat_limit            INTEGER NOT NULL DEFAULT 1,
    is_active             INTEGER NOT NULL DEFAULT 1,
    is_public             INTEGER NOT NULL DEFAULT 1,  -- false = bookable only via direct link
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS event_type_questions (
    id            TEXT PRIMARY KEY,
    event_type_id TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
    label         TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('text', 'select', 'checkbox')),
    options       TEXT,           -- JSON array; used for type='select'
    required      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS availability_rules (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type_id TEXT REFERENCES event_types(id) ON DELETE CASCADE,  -- NULL = global default
    day_of_week   INTEGER NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),  -- 0=Sun … 6=Sat
    start_time    TEXT NOT NULL,  -- HH:MM host-local wall-clock (§6.3)
    end_time      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS availability_overrides (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date         TEXT NOT NULL,   -- YYYY-MM-DD
    is_available INTEGER NOT NULL DEFAULT 0,
    start_time   TEXT,            -- HH:MM; only when is_available=1
    end_time     TEXT
);

CREATE TABLE IF NOT EXISTS calendar_connections (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL CHECK (provider IN ('google', 'microsoft', 'caldav')),
    access_token_enc  TEXT NOT NULL,   -- AES-GCM encrypted with CALNODE_ENCRYPTION_KEY (§15)
    refresh_token_enc TEXT,
    calendar_id       TEXT NOT NULL,
    check_conflicts   INTEGER NOT NULL DEFAULT 1,  -- include in free/busy checks (§8.3)
    is_destination    INTEGER NOT NULL DEFAULT 0,  -- write bookings to this calendar
    sync_token        TEXT,            -- incremental sync cursor
    channel_expires_at TEXT,           -- push-notification channel renewal (§13)
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS bookings (
    id                  TEXT PRIMARY KEY,
    event_type_id       TEXT NOT NULL REFERENCES event_types(id) ON DELETE RESTRICT,  -- explicit: historical bookings block event-type deletion
    host_id             TEXT NOT NULL REFERENCES users(id),
    start_at            TEXT NOT NULL,  -- UTC ISO 8601 (§6.3)
    end_at              TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'confirmed'
                          CHECK (status IN ('confirmed', 'cancelled')),
    cancellation_reason TEXT,
    location_value      TEXT,
    meeting_link        TEXT,
    external_event_id   TEXT,           -- calendar event id we created (for own-event exclusion §6.2)
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Double-booking guard (§6.4): no two active bookings for the same host at the same start time.
CREATE UNIQUE INDEX IF NOT EXISTS idx_bookings_no_double
    ON bookings (host_id, start_at) WHERE status != 'cancelled';

CREATE INDEX IF NOT EXISTS idx_bookings_host_time
    ON bookings (host_id, start_at, end_at) WHERE status = 'confirmed';

CREATE TABLE IF NOT EXISTS booking_attendees (
    id            TEXT PRIMARY KEY,
    booking_id    TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    email         TEXT NOT NULL,
    iana_timezone TEXT NOT NULL DEFAULT 'UTC',
    is_organizer  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS booking_answers (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL REFERENCES event_type_questions(id),
    value       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS webhooks (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id     TEXT REFERENCES teams(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    events      TEXT NOT NULL,   -- JSON array: ["booking.created","booking.cancelled",...]
    secret_enc  TEXT NOT NULL,   -- HMAC signing secret, AES-GCM encrypted with CALNODE_ENCRYPTION_KEY (§15)
    is_active   INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id                TEXT PRIMARY KEY,
    webhook_id        TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    booking_id        TEXT REFERENCES bookings(id),
    event             TEXT NOT NULL,
    payload           TEXT NOT NULL,   -- JSON
    status            TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'success', 'failed')),
    response_status   INTEGER,
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    last_attempted_at TEXT
);

CREATE TABLE IF NOT EXISTS jobs (
    id         TEXT PRIMARY KEY,
    type       TEXT NOT NULL,
    payload    TEXT NOT NULL,  -- JSON
    run_at     TEXT NOT NULL,  -- UTC ISO 8601; worker polls WHERE run_at <= now
    status     TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'running', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    last_error   TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_pending
    ON jobs (run_at) WHERE status = 'pending';

-- +goose Down

DROP INDEX IF EXISTS idx_jobs_pending;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhooks;
DROP TABLE IF EXISTS booking_answers;
DROP TABLE IF EXISTS booking_attendees;
DROP INDEX IF EXISTS idx_bookings_host_time;
DROP INDEX IF EXISTS idx_bookings_no_double;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS calendar_connections;
DROP TABLE IF EXISTS availability_overrides;
DROP TABLE IF EXISTS availability_rules;
DROP TABLE IF EXISTS event_type_questions;
DROP TABLE IF EXISTS event_types;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
