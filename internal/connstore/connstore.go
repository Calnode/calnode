// Package connstore holds the pieces of the calendar_connections upsert logic that
// are genuinely identical across every provider (Google, Microsoft, CalDAV) — the
// "-1 means any" filter convention, and the destination-claiming business rule. Each
// provider's row shape and OAuth/credential handling stay separate: Google/Microsoft
// store two OAuth tokens, CalDAV stores a username/password; Microsoft additionally
// tracks account_kind. Forcing those into one generic type would obscure more than it
// shares, so this package deliberately stays small.
package connstore

import (
	"context"
	"database/sql"
	"fmt"
)

// Execer is satisfied by both *sql.DB and *sql.Tx — ResolveFlags runs inside whichever
// transaction the caller already opened for its own upsert.
type Execer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// WhereClause builds the "AND check_conflicts = ? AND is_destination = ?" fragment
// (plus its args) for a calendar_connections query. -1 for either parameter means
// "any" — that filter is omitted. Every provider's connection-loading query
// (httpClient/loadConn) uses this exact convention when picking one destination or
// filtering to conflict-checked connections.
func WhereClause(checkConflicts, isDestination int) (fragment string, args []any) {
	if checkConflicts >= 0 {
		fragment += " AND check_conflicts = ?"
		args = append(args, checkConflicts)
	}
	if isDestination >= 0 {
		fragment += " AND is_destination = ?"
		args = append(args, isDestination)
	}
	return fragment, args
}

// ResolveFlags decides the check_conflicts/is_destination flags for a connection about
// to be (re-)saved, keyed by (userID, provider, accountEmail):
//   - If a row already exists for that account, its flags are PRESERVED (existing=true)
//     — a token refresh must never change whether an account is conflict-checked or the
//     destination.
//   - Otherwise it's a new connection: check_conflicts defaults to 1, and is_destination
//     is 1 only if the user has no destination connection yet — claimed here, inside the
//     caller's transaction, so two concurrent first-connects can't both claim it.
//
// tx must be the same transaction the caller uses for its subsequent DELETE+INSERT, so
// this read and that write are atomic together.
func ResolveFlags(ctx context.Context, tx Execer, userID, provider, accountEmail string) (checkConflicts, isDestination int, existing bool, err error) {
	checkConflicts = 1
	var ec, ed int
	switch err := tx.QueryRowContext(ctx,
		`SELECT check_conflicts, is_destination FROM calendar_connections
		 WHERE user_id = ? AND provider = ? AND account_email = ?`,
		userID, provider, accountEmail).Scan(&ec, &ed); err {
	case nil:
		return ec, ed, true, nil
	case sql.ErrNoRows:
		var destCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM calendar_connections WHERE user_id = ? AND is_destination = 1`,
			userID).Scan(&destCount); err != nil {
			return 0, 0, false, fmt.Errorf("connstore: dest check: %w", err)
		}
		if destCount == 0 {
			isDestination = 1
		}
		return checkConflicts, isDestination, false, nil
	default:
		return 0, 0, false, fmt.Errorf("connstore: flag lookup: %w", err)
	}
}
