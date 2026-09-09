package db

import (
	"errors"
	"slices"
	"strings"

	"modernc.org/sqlite"
)

// Constraint violations are the one class of database error Calnode routinely acts
// on rather than just reporting: a duplicate slug is a 409, an out-of-range value is
// a 400, a dangling reference is a 404. Deciding which is which was a substring match
// on SQLite's English message, which is invisible to every gate and, as the codes
// below show, cannot actually distinguish the two cases it is asked to.
//
// SQLite's extended result codes, as reported by (*sqlite.Error).Code().
//
// ⛔ SQLITE_CONSTRAINT_PRIMARYKEY is a SEPARATE code from SQLITE_CONSTRAINT_UNIQUE
// even though both carry the message "UNIQUE constraint failed". A text match cannot
// tell them apart at all, and matching only 2067 would silently stop recognising
// primary-key collisions. Calnode has one that matters:
// idempotency_keys.idempotency_key is a bare PRIMARY KEY, so every idempotent replay
// arrives as 1555. Both belong to IsUniqueViolation.
const (
	sqliteConstraintCheck      = 275  // SQLITE_CONSTRAINT_CHECK
	sqliteConstraintForeignKey = 787  // SQLITE_CONSTRAINT_FOREIGNKEY
	sqliteConstraintPrimaryKey = 1555 // SQLITE_CONSTRAINT_PRIMARYKEY
	sqliteConstraintUnique     = 2067 // SQLITE_CONSTRAINT_UNIQUE
)

// SQLite's message fragments, used only as a fallback — see violates.
const (
	sqliteUniqueText     = "UNIQUE constraint failed"
	sqliteCheckText      = "CHECK constraint failed"
	sqliteForeignKeyText = "FOREIGN KEY constraint failed"
)

// IsUniqueViolation reports whether err is a unique-constraint violation — a
// duplicate slug, a replayed idempotency key, a second booking at one host's exact
// start time. A primary-key collision counts.
func IsUniqueViolation(err error) bool {
	return violates(err, sqliteUniqueText, sqliteConstraintUnique, sqliteConstraintPrimaryKey)
}

// IsCheckViolation reports whether err is a CHECK-constraint violation, i.e. a value
// outside the set the column allows. Callers turn this into a 400, since the only way
// to reach it is a request carrying a value the handler did not validate.
func IsCheckViolation(err error) bool {
	return violates(err, sqliteCheckText, sqliteConstraintCheck)
}

// IsForeignKeyViolation reports whether err is a foreign-key violation — a reference
// to a row that does not exist, or a delete that would orphan one.
func IsForeignKeyViolation(err error) bool {
	return violates(err, sqliteForeignKeyText, sqliteConstraintForeignKey)
}

// violates classifies err: the driver's own error code when one is available, the
// message only when it is not.
//
// A driver error is a DEFINITE answer in both directions. A *sqlite.Error whose code
// does not match returns false and does not fall through to the text comparison —
// falling through would classify an error by whether its message happened to contain
// an English phrase, which is the fragility being removed. It would also reintroduce
// the primary-key trap in reverse: a 1555 error excluded by code would be readmitted
// by its "UNIQUE constraint failed" message.
//
// The text fallback is deliberate rather than vestigial. It covers an error that
// reaches here without the concrete driver type still attached — a driver release
// that changes its error type, a layer that reformats an error into a plain one
// instead of wrapping it. In that case the message is the only signal left, and
// answering from it beats answering "not a constraint violation" and returning a 500.
func violates(err error, sqliteText string, sqliteCodes ...int) bool {
	if err == nil {
		return false
	}

	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return slices.Contains(sqliteCodes, sqliteErr.Code())
	}

	return strings.Contains(err.Error(), sqliteText)
}
