-- +goose Up
-- Indexes for the paginated bookings list (§9).
--
-- The two indexes that existed both lead on host_id and are partial
-- (idx_bookings_no_double, idx_bookings_host_time), which serves the double-book
-- guard well and the list not at all. Every paged query planned as:
--
--     SCAN bookings USING INDEX idx_bookings_no_double
--     USE TEMP B-TREE FOR ORDER BY
--
-- i.e. sort the entire matching set to hand back 25 rows. Paginating the API without
-- this makes the response smaller but not the work: the cost still grows with every
-- booking ever made.
--
-- With (start_at, id) the same query plans as a plain index walk and stops at the
-- LIMIT, no sort:
--
--     SCAN bookings USING INDEX idx_bookings_start_at
--
-- id is included as the tiebreaker the list orders by; start_at is not unique, and
-- without a second key two bookings at the same time can swap between pages and one
-- of them is never shown.
CREATE INDEX IF NOT EXISTS idx_bookings_start_at
    ON bookings (start_at, id);

-- Filtering to one event type went from a full scan to a search. The trailing
-- ORDER BY term still costs a small in-memory sort within equal start_at groups,
-- which is not worth another index.
CREATE INDEX IF NOT EXISTS idx_bookings_event_type_start
    ON bookings (event_type_id, start_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_bookings_start_at;
DROP INDEX IF EXISTS idx_bookings_event_type_start;
