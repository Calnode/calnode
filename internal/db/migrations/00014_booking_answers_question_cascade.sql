-- +goose Up
-- booking_answers.question_id referenced event_type_questions(id) with no
-- ON DELETE rule, so deleting an intake question that already had responses
-- (or deleting an event type that owns such questions) failed with a foreign-key
-- violation surfaced as a 500. SQLite can't ALTER a constraint, so recreate the
-- table with ON DELETE CASCADE. booking_answers is a leaf (nothing references
-- it), so this is safe inside the migration transaction with foreign_keys on.
CREATE TABLE booking_answers_new (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL REFERENCES event_type_questions(id) ON DELETE CASCADE,
    value       TEXT NOT NULL
);
INSERT INTO booking_answers_new (id, booking_id, question_id, value)
    SELECT id, booking_id, question_id, value FROM booking_answers;
DROP TABLE booking_answers;
ALTER TABLE booking_answers_new RENAME TO booking_answers;

-- +goose Down
CREATE TABLE booking_answers_old (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL REFERENCES event_type_questions(id),
    value       TEXT NOT NULL
);
INSERT INTO booking_answers_old (id, booking_id, question_id, value)
    SELECT id, booking_id, question_id, value FROM booking_answers;
DROP TABLE booking_answers;
ALTER TABLE booking_answers_old RENAME TO booking_answers;
