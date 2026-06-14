package booking

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// Service handles booking creation and lifecycle.
type Service struct {
	db *sql.DB
}

// New returns a Service backed by db.
func New(db *sql.DB) *Service {
	return &Service{db: db}
}

// Create inserts a new confirmed booking inside a transaction.
// It checks for overlapping bookings for every host in p.HostIDs before
// inserting, satisfying the double-booking guard described in §6.4.
// The partial unique index on (host_id, start_at) acts as a secondary guard
// for exact-start-time collisions; both paths return ErrDoubleBooked.
func (s *Service) Create(ctx context.Context, p CreateParams) (*Booking, error) {
	if len(p.HostIDs) == 0 {
		return nil, fmt.Errorf("booking: HostIDs must not be empty")
	}

	startStr := p.StartAt.UTC().Format(time.RFC3339Nano)
	endStr := p.EndAt.UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("booking: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Two intervals [A,B) and [C,D) overlap when A < D and C < B.
	const overlapQ = `
		SELECT COUNT(*) FROM bookings
		WHERE host_id = ? AND status != 'cancelled'
		  AND start_at < ? AND end_at > ?`

	for _, hostID := range p.HostIDs {
		var n int
		if err := tx.QueryRowContext(ctx, overlapQ, hostID, endStr, startStr).Scan(&n); err != nil {
			return nil, fmt.Errorf("booking: overlap check: %w", err)
		}
		if n > 0 {
			return nil, ErrDoubleBooked
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	bookingID := uid.New()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO bookings
		  (id, event_type_id, host_id, start_at, end_at, status, location_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'confirmed', ?, ?, ?)`,
		bookingID, p.EventTypeID, p.HostIDs[0], startStr, endStr, p.LocationValue, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDoubleBooked
		}
		return nil, fmt.Errorf("booking: insert: %w", err)
	}

	tz := p.Organizer.IANATimezone
	if tz == "" {
		tz = "UTC"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
		VALUES (?, ?, ?, ?, ?, 1)`,
		uid.New(), bookingID, p.Organizer.Name, p.Organizer.Email, tz)
	if err != nil {
		return nil, fmt.Errorf("booking: insert attendee: %w", err)
	}

	for _, ans := range p.Answers {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO booking_answers (id, booking_id, question_id, value)
			VALUES (?, ?, ?, ?)`,
			uid.New(), bookingID, ans.QuestionID, ans.Value)
		if err != nil {
			return nil, fmt.Errorf("booking: insert answer: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("booking: commit: %w", err)
	}

	nowT, _ := time.Parse(time.RFC3339Nano, now)
	return &Booking{
		ID:            bookingID,
		EventTypeID:   p.EventTypeID,
		HostID:        p.HostIDs[0],
		StartAt:       p.StartAt.UTC(),
		EndAt:         p.EndAt.UTC(),
		Status:        "confirmed",
		LocationValue: p.LocationValue,
		CreatedAt:     nowT,
		UpdatedAt:     nowT,
	}, nil
}

// Cancel marks a booking as cancelled. Returns ErrNotFound if the booking does
// not exist and ErrAlreadyCancelled if it is already in that state.
func (s *Service) Cancel(ctx context.Context, id, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE bookings
		SET status = 'cancelled', cancellation_reason = ?, updated_at = ?
		WHERE id = ? AND status != 'cancelled'`,
		reason, now, id)
	if err != nil {
		return fmt.Errorf("booking: cancel: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("booking: cancel rows: %w", err)
	}
	if n == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM bookings WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("booking: cancel existence check: %w", err)
		}
		if exists == 0 {
			return ErrNotFound
		}
		return ErrAlreadyCancelled
	}
	return nil
}

// Get returns a single booking by ID.
func (s *Service) Get(ctx context.Context, id string) (*Booking, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, event_type_id, host_id, start_at, end_at, status,
		       COALESCE(cancellation_reason, ''), COALESCE(location_value, ''),
		       created_at, updated_at
		FROM bookings WHERE id = ?`, id)
	return scanBooking(row)
}

// ListByHost returns all non-cancelled bookings for a host, ordered by start time.
func (s *Service) ListByHost(ctx context.Context, hostID string) ([]Booking, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_type_id, host_id, start_at, end_at, status,
		       COALESCE(cancellation_reason, ''), COALESCE(location_value, ''),
		       created_at, updated_at
		FROM bookings
		WHERE host_id = ? AND status != 'cancelled'
		ORDER BY start_at`, hostID)
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

type scanner interface {
	Scan(dest ...any) error
}

func scanBooking(s scanner) (*Booking, error) {
	var b Booking
	var startStr, endStr, createdStr, updatedStr string

	err := s.Scan(
		&b.ID, &b.EventTypeID, &b.HostID,
		&startStr, &endStr, &b.Status,
		&b.CancellationReason, &b.LocationValue,
		&createdStr, &updatedStr,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("booking: scan: %w", err)
	}

	var parseErr error
	if b.StartAt, parseErr = time.Parse(time.RFC3339Nano, startStr); parseErr != nil {
		return nil, fmt.Errorf("booking: parse start_at %q: %w", startStr, parseErr)
	}
	if b.EndAt, parseErr = time.Parse(time.RFC3339Nano, endStr); parseErr != nil {
		return nil, fmt.Errorf("booking: parse end_at %q: %w", endStr, parseErr)
	}
	if b.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdStr); parseErr != nil {
		return nil, fmt.Errorf("booking: parse created_at %q: %w", createdStr, parseErr)
	}
	if b.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedStr); parseErr != nil {
		return nil, fmt.Errorf("booking: parse updated_at %q: %w", updatedStr, parseErr)
	}
	return &b, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
