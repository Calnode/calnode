-- +goose Up
-- needs_sync flags a booking_hosts row whose calendar event is known to be at the
-- WRONG time: set when an inline reschedule move (gcal UpdateEvent) fails, cleared
-- when it succeeds (inline or via the reconciler). The reconciler re-applies the
-- move for flagged rows — closing the one gap the presence/absence passes can't see,
-- since drift can't be inferred from booking state alone. 0 = in sync.
ALTER TABLE booking_hosts ADD COLUMN needs_sync INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE booking_hosts DROP COLUMN needs_sync;
