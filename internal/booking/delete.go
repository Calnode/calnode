package booking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Errors returned by Delete when a booking must not be erased yet. Each names the step
// that comes first, so the admin UI can say what to do instead of a bare refusal.
var (
	// ErrDeleteUpcoming: a confirmed booking that has not ended yet. Deleting it would make
	// the meeting vanish for the host while the booker still holds the invite; cancel it
	// first (which tells the booker, removes the calendar event, refunds and fires the
	// cancellation webhook), then delete.
	ErrDeleteUpcoming = errors.New("booking: cancel the upcoming booking before deleting it")
	// ErrDeletePaid: the booking carries a payment record. Payment records have their own
	// retention duties, so this path refuses rather than silently dropping one.
	ErrDeletePaid = errors.New("booking: a paid booking keeps its record")
	// ErrDeleteRecordings: recordings are files in object storage; removing only the row
	// would orphan the file. Delete them on the Recordings page first.
	ErrDeleteRecordings = errors.New("booking: delete the booking's recordings first")
	// ErrDeleteCalendarPending: the booking was cancelled but a host's calendar event is still
	// recorded as present (removal failed and the reconciler will retry). Deleting the row now
	// would lose the id needed to remove that event, leaving it on the calendar for good.
	ErrDeleteCalendarPending = errors.New("booking: the cancelled booking's calendar event is still being removed")
)

// Delete permanently erases a booking and the personal data stored with it, for an
// erasure request (GDPR Art. 17). It sends no email and fires no webhook: the person
// asked for their data to be removed, not to be told about a cancellation.
//
// Removed in one transaction:
//   - the booking row, and by ON DELETE CASCADE its attendees, form answers, manage
//     tokens and host assignments;
//   - rows that reference it without a cascade: webhook deliveries (their payload holds
//     the attendee's name and email) and the jobs that would deliver them, reminder jobs,
//     idempotency records (their stored response holds the booking), transcripts and notes.
//
// Not touched: a calendar event that still exists for a past booking stays on the host's
// calendar (removing it through the provider would email the guest a cancellation), and
// database backups keep the data until their own retention ends. Both are documented for
// the operator.
func (s *Service) Delete(ctx context.Context, id string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("booking: delete: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status, endStr, paymentStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT status, end_at, payment_status FROM bookings WHERE id = ?`, id).
		Scan(&status, &endStr, &paymentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("booking: delete: load: %w", err)
	}
	endAt, err := time.Parse(time.RFC3339Nano, endStr)
	if err != nil {
		return fmt.Errorf("booking: delete: parse end_at %q: %w", endStr, err)
	}
	if status != "cancelled" && endAt.After(now) {
		return ErrDeleteUpcoming
	}
	if paymentStatus != "" && paymentStatus != "none" {
		return ErrDeletePaid
	}

	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recordings WHERE booking_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("booking: delete: count recordings: %w", err)
	}
	if n > 0 {
		return ErrDeleteRecordings
	}
	if status == "cancelled" {
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM booking_hosts
			WHERE booking_id = ? AND COALESCE(external_event_id, '') != ''`, id).Scan(&n); err != nil {
			return fmt.Errorf("booking: delete: count calendar events: %w", err)
		}
		if n > 0 {
			return ErrDeleteCalendarPending
		}
	}

	// Order matters: jobs point at webhook deliveries by id inside their JSON payload, and
	// webhook_deliveries.booking_id references bookings without a cascade.
	steps := []struct{ what, query string }{
		{"webhook jobs", `DELETE FROM jobs WHERE type = 'webhook.deliver'
			AND json_extract(payload, '$.webhook_delivery_id') IN (SELECT id FROM webhook_deliveries WHERE booking_id = ?)`},
		{"booking jobs", `DELETE FROM jobs WHERE json_valid(payload) AND json_extract(payload, '$.booking_id') = ?`},
		{"webhook deliveries", `DELETE FROM webhook_deliveries WHERE booking_id = ?`},
		{"idempotency keys", `DELETE FROM idempotency_keys WHERE booking_id = ?`},
		{"transcripts", `DELETE FROM transcripts WHERE booking_id = ?`},
		{"notes", `DELETE FROM notes WHERE booking_id = ?`},
		{"booking", `DELETE FROM bookings WHERE id = ?`},
	}
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, step.query, id); err != nil {
			return fmt.Errorf("booking: delete %s: %w", step.what, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("booking: delete: commit: %w", err)
	}
	return nil
}
