-- +goose Up
-- Multi-host bookings: a booking can have several hosts (Group/collective, or a
-- round-robin pick plus fixed hosts). bookings.host_id stays the *primary* host
-- (for the double-book guard + back-compat); booking_hosts records everyone who
-- attends. See docs/teams-and-routing.md.
CREATE TABLE booking_hosts (
    id         TEXT    PRIMARY KEY,
    booking_id TEXT    NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users(id),
    is_primary INTEGER NOT NULL DEFAULT 0,
    UNIQUE(booking_id, user_id)
);

CREATE INDEX idx_booking_hosts_booking ON booking_hosts(booking_id);
CREATE INDEX idx_booking_hosts_user ON booking_hosts(user_id);

-- Backfill: every existing booking gets its host as the single primary host row,
-- so host resolution is uniform from day one.
INSERT INTO booking_hosts (id, booking_id, user_id, is_primary)
SELECT lower(hex(randomblob(16))), id, host_id, 1 FROM bookings;

-- +goose Down
DROP TABLE IF EXISTS booking_hosts;
