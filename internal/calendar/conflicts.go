package calendar

import (
	"context"
	"database/sql"
)

// ConflictCalendarIDs resolves which calendar IDs of one connected account must be checked for
// conflicts, honouring the user's per-account sub-calendar selection (connection_calendars).
//
// Semantics, so accounts connected before (or without) using the picker keep working:
//   - The account has saved selections (>=1 connection_calendars row): return the calendar_ids
//     marked check_conflicts=1. This may be EMPTY, meaning the user deliberately opted the whole
//     account out of conflict checking.
//   - No saved selections at all: return []string{fallbackCalID} — the account's bound calendar —
//     reproducing the pre-picker "every connected calendar is its one bound calendar" behaviour.
//
// The caller must have no open rows cursor on the shared DB pool when calling this (the pool is
// single-connection): drain and Close() any cursor first.
func ConflictCalendarIDs(ctx context.Context, db *sql.DB, provider, userID, accountEmail, fallbackCalID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT calendar_id, check_conflicts FROM connection_calendars
		 WHERE user_id = ? AND provider = ? AND account_email = ?`,
		userID, provider, accountEmail)
	if err != nil {
		return nil, err
	}
	var selected []string
	configured := false
	for rows.Next() {
		var calID string
		var checkConflicts int
		if err := rows.Scan(&calID, &checkConflicts); err != nil {
			rows.Close() //nolint:errcheck,gosec
			return nil, err
		}
		configured = true
		if checkConflicts != 0 {
			selected = append(selected, calID)
		}
	}
	rows.Close() //nolint:errcheck,gosec
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !configured {
		return []string{fallbackCalID}, nil
	}
	return selected, nil
}
