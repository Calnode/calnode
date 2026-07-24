package calendar

import (
	"context"
	"fmt"

	"github.com/calnode/calnode/internal/uid"
)

// resolveConn maps a calendar_connections.id to its (provider, accountEmail) for the user.
func (s *Service) resolveConn(ctx context.Context, userID, connID string) (provider, accountEmail string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT provider, COALESCE(account_email,'') FROM calendar_connections WHERE id = ? AND user_id = ?`,
		connID, userID).Scan(&provider, &accountEmail)
	return provider, accountEmail, err
}

// AccountCalendars lists every calendar in the connection's account, each annotated with the
// user's saved check_conflicts / is_destination choice (from connection_calendars). An
// unsaved calendar defaults to conflict-checked only if it's the account's primary.
func (s *Service) AccountCalendars(ctx context.Context, userID, connID string) ([]CalendarSelection, error) {
	provider, accountEmail, err := s.resolveConn(ctx, userID, connID)
	if err != nil {
		return nil, err
	}
	p := s.providers[provider]
	if p == nil {
		return nil, fmt.Errorf("calendar: provider %q not configured", provider)
	}
	cals, err := p.ListCalendars(ctx, userID, accountEmail)
	if err != nil {
		return nil, err
	}

	// Load saved selections into a map first (single-connection pool: don't hold a cursor).
	type sel struct{ cc, dest bool }
	saved := map[string]sel{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT calendar_id, check_conflicts, is_destination FROM connection_calendars
		 WHERE user_id = ? AND provider = ? AND account_email = ?`, userID, provider, accountEmail)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cid string
		var cc, dest int
		if err := rows.Scan(&cid, &cc, &dest); err != nil {
			rows.Close() //nolint:errcheck,gosec
			return nil, err
		}
		saved[cid] = sel{cc != 0, dest != 0}
	}
	rows.Close() //nolint:errcheck,gosec
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]CalendarSelection, 0, len(cals))
	for _, c := range cals {
		cs := CalendarSelection{CalendarInfo: c}
		if s2, ok := saved[c.ID]; ok {
			cs.CheckConflicts = s2.cc
			cs.IsDestination = s2.dest
		} else {
			cs.CheckConflicts = c.Primary // sensible default for a never-configured account
		}
		out = append(out, cs)
	}
	return out, nil
}

// SetAccountCalendars replaces the saved selection for the connection's account. At most one
// calendar across the whole user may be the destination (enforced by clearing others first).
func (s *Service) SetAccountCalendars(ctx context.Context, userID, connID string, sels []CalendarSelection) error {
	provider, accountEmail, err := s.resolveConn(ctx, userID, connID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	claimsDest := false
	for _, c := range sels {
		if c.IsDestination {
			claimsDest = true
			break
		}
	}
	if claimsDest {
		if _, err := tx.ExecContext(ctx,
			`UPDATE connection_calendars SET is_destination = 0 WHERE user_id = ?`, userID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM connection_calendars WHERE user_id = ? AND provider = ? AND account_email = ?`,
		userID, provider, accountEmail); err != nil {
		return err
	}
	for _, c := range sels {
		if !c.CheckConflicts && !c.IsDestination {
			continue // a fully-off calendar needs no row
		}
		cc, dest := 0, 0
		if c.CheckConflicts {
			cc = 1
		}
		if c.IsDestination {
			dest = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, name, check_conflicts, is_destination)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uid.New(), userID, provider, accountEmail, c.ID, c.Name, cc, dest); err != nil {
			return err
		}
	}
	return tx.Commit()
}
