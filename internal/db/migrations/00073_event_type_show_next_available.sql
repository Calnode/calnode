-- +goose Up
-- Whether the booking page shows the next available time above the time list, as a
-- button that jumps to that day. Proposed in discussion #254.
--
-- Default 0, so nothing changes for an existing event type. It follows
-- show_taken_slots (00057) rather than being always on: the line is computed from the
-- same public slots endpoint a visitor can already read, so it discloses nothing new,
-- but a host whose page is meant to be browsed day by day may not want it.
ALTER TABLE event_types ADD COLUMN show_next_available INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE event_types DROP COLUMN show_next_available;
