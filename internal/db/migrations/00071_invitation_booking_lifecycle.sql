-- +goose Up
-- Continue the already-applied invitation foundation without renumbering 00070.
ALTER TABLE scheduling_invitations ADD COLUMN updated_at TEXT;
ALTER TABLE scheduling_invitations ADD COLUMN location_type TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduling_invitations ADD COLUMN location_value TEXT NOT NULL DEFAULT '';
UPDATE scheduling_invitations SET updated_at = created_at,
    location_type = (SELECT location_type FROM event_types WHERE id = event_type_id),
    location_value = COALESCE((SELECT location_value FROM event_types WHERE id = event_type_id), '');
ALTER TABLE bookings ADD COLUMN scheduling_invitation_id TEXT REFERENCES scheduling_invitations(id);
CREATE UNIQUE INDEX idx_bookings_scheduling_invitation ON bookings(scheduling_invitation_id)
    WHERE scheduling_invitation_id IS NOT NULL;
UPDATE bookings SET scheduling_invitation_id = (SELECT id FROM scheduling_invitations WHERE booking_id = bookings.id)
    WHERE id IN (SELECT booking_id FROM scheduling_invitations WHERE booking_id IS NOT NULL);

-- Transactional outbox: state transitions survive a process stopping between
-- commit and webhook enqueue. The existing worker delivers/retries signed events.
CREATE TABLE scheduling_invitation_events (
    id TEXT PRIMARY KEY,
    invitation_id TEXT NOT NULL REFERENCES scheduling_invitations(id),
    event TEXT NOT NULL,
    created_at TEXT NOT NULL,
    payload TEXT NOT NULL,
    dispatched_at TEXT,
    UNIQUE(invitation_id, event)
);
CREATE INDEX idx_invitation_events_pending ON scheduling_invitation_events(created_at)
    WHERE dispatched_at IS NULL;

-- Capture transition-time correlation and intervals, rather than reconstructing
-- an earlier event from whatever state the worker happens to read later.
CREATE VIEW scheduling_invitation_event_data AS
SELECT i.id, json_object(
    'scheduling_invitation_id', i.id, 'event_type_id', i.event_type_id,
    'status', i.status, 'duration_minutes', i.duration_minutes,
    'expires_at', i.expires_at, 'booking_id', i.booking_id,
    'external', json_object('system', i.external_system, 'reference', i.external_reference, 'url', i.external_url),
    'start_at', b.start_at, 'end_at', b.end_at, 'host_id', b.host_id,
    'location_type', COALESCE(b.location_type, i.location_type),
    'location_value', COALESCE(b.location_value, i.location_value),
    'hosts', json((SELECT json_group_array(json_object('id', bh.user_id, 'name', u.name, 'is_primary', json(CASE WHEN bh.is_primary = 1 THEN 'true' ELSE 'false' END)))
        FROM booking_hosts bh JOIN users u ON u.id = bh.user_id WHERE bh.booking_id = i.booking_id))
) AS payload
FROM scheduling_invitations i LEFT JOIN bookings b ON b.id = i.booking_id;

-- +goose StatementBegin
CREATE TRIGGER scheduling_invitation_created AFTER INSERT ON scheduling_invitations
WHEN NEW.status = 'active'
BEGIN
    INSERT INTO scheduling_invitation_events (id, invitation_id, event, created_at, payload)
    SELECT lower(hex(randomblob(16))), id, 'scheduling_invitation.created', NEW.created_at, payload
    FROM scheduling_invitation_event_data WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER scheduling_invitation_transition AFTER UPDATE OF status ON scheduling_invitations
WHEN OLD.status = 'active' AND NEW.status IN ('booked', 'cancelled', 'expired')
BEGIN
    INSERT INTO scheduling_invitation_events (id, invitation_id, event, created_at, payload)
    SELECT lower(hex(randomblob(16))), id, 'scheduling_invitation.' || NEW.status, NEW.updated_at, payload
    FROM scheduling_invitation_event_data WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER scheduling_invitation_transition;
DROP TRIGGER scheduling_invitation_created;
DROP VIEW scheduling_invitation_event_data;
DROP TABLE scheduling_invitation_events;
DROP INDEX idx_bookings_scheduling_invitation;
ALTER TABLE bookings DROP COLUMN scheduling_invitation_id;
ALTER TABLE scheduling_invitations DROP COLUMN location_value;
ALTER TABLE scheduling_invitations DROP COLUMN location_type;
ALTER TABLE scheduling_invitations DROP COLUMN updated_at;
