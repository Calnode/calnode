-- +goose Up
-- Directory-order PR #127 is expected to land first at version 00070.
-- Discussion #47 / issue #92: event types remain reusable defaults; invitations
-- snapshot authorized scheduling values. NULL duration limits preserve the
-- existing fixed-duration policy, including when the default duration changes.
ALTER TABLE event_types ADD COLUMN min_duration_minutes INTEGER
    CHECK (min_duration_minutes IS NULL OR min_duration_minutes > 0);
ALTER TABLE event_types ADD COLUMN max_duration_minutes INTEGER
    CHECK (max_duration_minutes IS NULL OR max_duration_minutes > 0);
ALTER TABLE event_types ADD COLUMN duration_increment_minutes INTEGER
    CHECK (
        (min_duration_minutes IS NULL AND max_duration_minutes IS NULL AND duration_increment_minutes IS NULL)
        OR (min_duration_minutes IS NOT NULL AND max_duration_minutes IS NOT NULL
            AND duration_increment_minutes IS NOT NULL AND duration_increment_minutes > 0
            AND max_duration_minutes >= min_duration_minutes)
    );

CREATE TABLE scheduling_invitations (
    id TEXT PRIMARY KEY,
    event_type_id TEXT NOT NULL REFERENCES event_types(id),
    created_by TEXT NOT NULL REFERENCES users(id),
    recipient_name TEXT NOT NULL DEFAULT '',
    recipient_email TEXT NOT NULL,
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    slot_interval_minutes INTEGER NOT NULL CHECK (slot_interval_minutes > 0),
    buffer_before_minutes INTEGER NOT NULL CHECK (buffer_before_minutes >= 0),
    buffer_after_minutes INTEGER NOT NULL CHECK (buffer_after_minutes >= 0),
    min_notice_minutes INTEGER NOT NULL CHECK (min_notice_minutes >= 0),
    max_future_days INTEGER NOT NULL CHECK (max_future_days >= 0),
    routing_mode TEXT NOT NULL CHECK (routing_mode IN ('fixed', 'round_robin', 'collective', 'priority')),
    rr_strategy TEXT NOT NULL CHECK (rr_strategy IN ('even', 'soonest', 'priority')),
    available_from TEXT,
    available_until TEXT,
    availability_timezone TEXT NOT NULL DEFAULT 'UTC',
    external_system TEXT NOT NULL DEFAULT '',
    external_reference TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    delivery TEXT NOT NULL DEFAULT 'external' CHECK (delivery IN ('external', 'calnode')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('draft', 'active', 'booked', 'cancelled', 'expired')),
    booking_id TEXT UNIQUE REFERENCES bookings(id),
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CHECK ((status = 'booked' AND booking_id IS NOT NULL) OR (status != 'booked' AND booking_id IS NULL))
);
CREATE INDEX idx_scheduling_invitations_creator ON scheduling_invitations(created_by, created_at);
CREATE INDEX idx_scheduling_invitations_event ON scheduling_invitations(event_type_id);

-- The same host-role vocabulary and priority ordering as event_type_hosts.
-- These are immutable snapshot rows, not a separate assignment algorithm.
CREATE TABLE scheduling_invitation_hosts (
    invitation_id TEXT NOT NULL REFERENCES scheduling_invitations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    role TEXT NOT NULL CHECK (role IN ('required', 'rotation', 'optional')),
    priority INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (invitation_id, user_id)
);

-- One bearer credential per invitation. No public redemption endpoint in this
-- slice: a later booking transaction must claim it and set booked atomically.
CREATE TABLE scheduling_invitation_tokens (
    token_hash TEXT PRIMARY KEY CHECK (length(token_hash) = 64),
    invitation_id TEXT NOT NULL UNIQUE REFERENCES scheduling_invitations(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    consumed_at TEXT
);

-- +goose Down
DROP TABLE scheduling_invitation_tokens;
DROP TABLE scheduling_invitation_hosts;
DROP TABLE scheduling_invitations;
ALTER TABLE event_types DROP COLUMN duration_increment_minutes;
ALTER TABLE event_types DROP COLUMN max_duration_minutes;
ALTER TABLE event_types DROP COLUMN min_duration_minutes;
