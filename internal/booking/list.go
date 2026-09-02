package booking

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ListFilter narrows a booking listing. The zero value lists every non-cancelled
// booking in the workspace, ordered soonest-first - what the admin "All bookings"
// view wants.
//
// Every field is applied in SQL rather than by the caller. The bookings table grows
// without bound and the pool is a single connection (ARCHITECTURE §17), so filtering
// after the fact means loading an entire workspace to display one page of it.
type ListFilter struct {
	// ViewerID restricts the listing to bookings this user hosts, either as the
	// primary host or as an assigned host in booking_hosts - so Group attendees see
	// meetings they are on, not only the ones they lead. Empty lists the whole
	// workspace; callers MUST gate that on the admin role.
	ViewerID string

	EventTypeID string // event_types.id, not the slug
	HostID      string // a specific host, resolved the same way ViewerID is
	TeamID      string // any member of this team hosts the booking

	// Status matches exactly when set. When empty, cancelled bookings are excluded:
	// that is the default every existing caller depends on, and it is why passing an
	// explicit status is the only way to see cancelled bookings at all.
	Status string

	From time.Time // start_at >= From
	To   time.Time // start_at <  To

	// When is "upcoming" or "past", keyed on end_at rather than start_at so a meeting
	// that has begun but not finished still counts as upcoming. Needs Now set.
	When string
	Now  time.Time

	// Order is "desc" for most-recent-first (what the Past view wants); anything else
	// sorts soonest-first.
	Order string

	Limit  int // 0 means unlimited - only the internal wrappers should use that
	Offset int
}

// Counts reports how many bookings match a filter on each side of "now", ignoring
// the filter's own When, Limit and Offset. The bookings page needs both numbers for
// its tab labels, and deriving them client-side is exactly what pagination removes.
type Counts struct {
	Upcoming int
	Past     int
}

// Total is every booking matching the filter, on both sides of now.
func (c Counts) Total() int { return c.Upcoming + c.Past }

// hostsBooking matches bookings a given user hosts. Takes the user id twice.
const hostsBooking = `(bookings.host_id = ? OR EXISTS (
		SELECT 1 FROM booking_hosts bh
		WHERE bh.booking_id = bookings.id AND bh.user_id = ?))`

// teamHostsBooking matches bookings hosted by any member of a team. Teams associate
// with bookings through people, not through the event type: event_types.team_id
// exists but nothing writes it, because a team is a shortcut for populating
// event_type_hosts rather than a stored relationship. A user in two teams therefore
// has their bookings counted under both, which is a known and accepted wrinkle.
const teamHostsBooking = `EXISTS (
		SELECT 1 FROM team_members tm
		WHERE tm.team_id = ?
		  AND (tm.user_id = bookings.host_id
		       OR EXISTS (SELECT 1 FROM booking_hosts bh
		                  WHERE bh.booking_id = bookings.id AND bh.user_id = tm.user_id)))`

// sqlTime renders t the way the bookings table stores start_at/end_at.
//
// Comparisons against these columns are lexicographic on an RFC3339 string, which is
// only order-preserving when both sides agree about fractional seconds - RFC3339Nano
// trims trailing zeros, and '.' sorts below 'Z'. Truncating to the second keeps the
// boundary free of a fractional part; booking times are slot-aligned so they have no
// fraction either, leaving at most a sub-second classification error at the exact
// boundary. The same convention is already relied on by hostBusy.
func sqlTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// where builds the shared WHERE clause and its bound arguments. Every fragment is a
// compile-time literal and every value travels as a bound parameter, so the assembled
// string never contains caller input.
//
// includeWhen exists because Counts needs the same predicate set with the
// upcoming/past split removed.
func (f ListFilter) where(includeWhen bool) (string, []any) {
	var conds []string
	var args []any

	if f.Status != "" {
		conds = append(conds, "bookings.status = ?")
		args = append(args, f.Status)
	} else {
		conds = append(conds, "bookings.status != 'cancelled'")
	}
	if f.ViewerID != "" {
		conds = append(conds, hostsBooking)
		args = append(args, f.ViewerID, f.ViewerID)
	}
	if f.HostID != "" {
		conds = append(conds, hostsBooking)
		args = append(args, f.HostID, f.HostID)
	}
	if f.TeamID != "" {
		conds = append(conds, teamHostsBooking)
		args = append(args, f.TeamID)
	}
	if f.EventTypeID != "" {
		conds = append(conds, "bookings.event_type_id = ?")
		args = append(args, f.EventTypeID)
	}
	if !f.From.IsZero() {
		conds = append(conds, "bookings.start_at >= ?")
		args = append(args, sqlTime(f.From))
	}
	if !f.To.IsZero() {
		conds = append(conds, "bookings.start_at < ?")
		args = append(args, sqlTime(f.To))
	}
	if includeWhen && f.When != "" && !f.Now.IsZero() {
		switch f.When {
		case "past":
			conds = append(conds, "bookings.end_at < ?")
		default: // "upcoming"
			conds = append(conds, "bookings.end_at >= ?")
		}
		args = append(args, sqlTime(f.Now))
	}
	return "WHERE " + strings.Join(conds, "\n\t\t  AND "), args
}

// List returns the bookings matching f, ordered and paginated in SQL.
func (s *Service) List(ctx context.Context, f ListFilter) ([]Booking, error) {
	whereSQL, args := f.where(true)

	order := "ASC"
	if f.Order == "desc" {
		order = "DESC"
	}
	// start_at is not unique, so a second key keeps paging stable across requests -
	// without it two bookings at the same time can swap places between pages and one
	// of them is never shown.
	q := `SELECT ` + bookingColumns + ` FROM bookings
		` + whereSQL + `
		ORDER BY start_at ` + order + `, id ` + order //#nosec G202 -- whereSQL is assembled from literal fragments only; every value is bound via args

	if f.Limit > 0 {
		q += "\n\t\tLIMIT ? OFFSET ?"
		args = append(args, f.Limit, max(f.Offset, 0))
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("booking: list: %w", err)
	}
	defer rows.Close()

	var out []Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Counts returns how many bookings match f on each side of now. f's When, Limit and
// Offset are ignored, so one call labels both tabs.
//
// Conditional aggregation rather than two queries: the pool is a single connection,
// so a second round trip costs more than the CASE does.
func (s *Service) Counts(ctx context.Context, f ListFilter) (Counts, error) {
	whereSQL, args := f.where(false)
	now := f.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	q := `SELECT
		    COALESCE(SUM(CASE WHEN end_at >= ? THEN 1 ELSE 0 END), 0),
		    COALESCE(SUM(CASE WHEN end_at <  ? THEN 1 ELSE 0 END), 0)
		FROM bookings
		` + whereSQL //#nosec G202 -- whereSQL is assembled from literal fragments only; every value is bound via args

	nowStr := sqlTime(now)
	var c Counts
	err := s.db.QueryRowContext(ctx, q, append([]any{nowStr, nowStr}, args...)...).
		Scan(&c.Upcoming, &c.Past)
	if err != nil {
		return Counts{}, fmt.Errorf("booking: counts: %w", err)
	}
	return c, nil
}
