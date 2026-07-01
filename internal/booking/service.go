package booking

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
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

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Select hosts. Round-robin picks one *free* candidate from the rotation pool
	// per p.RRStrategy (free candidates stay in priority order). Any
	// other mode requires every HostID free and all of them attend (Normal = one
	// host; Group/collective = several). Optional hosts join only if free.
	// `assigned` is everyone who will attend; `chosenHost` is the primary.
	var chosenHost string
	var assigned []string
	if p.RoutingMode == "round_robin" {
		// Fixed hosts always attend — all must be free.
		for _, hostID := range p.RequiredHosts {
			busy, err := hostBusy(ctx, tx, hostID, startStr, endStr, "")
			if err != nil {
				return nil, fmt.Errorf("booking: overlap check: %w", err)
			}
			if busy {
				return nil, ErrDoubleBooked
			}
			assigned = append(assigned, hostID)
		}
		// Rotation pool — pick exactly one free host by strategy.
		var free []string
		for _, hostID := range p.HostIDs {
			busy, err := hostBusy(ctx, tx, hostID, startStr, endStr, "")
			if err != nil {
				return nil, fmt.Errorf("booking: overlap check: %w", err)
			}
			if !busy {
				free = append(free, hostID)
			}
		}
		if len(free) == 0 {
			return nil, ErrDoubleBooked
		}
		chosen, err := pickRotationHost(ctx, tx, p.EventTypeID, p.RRStrategy, free, now)
		if err != nil {
			return nil, err
		}
		chosenHost = chosen
		assigned = append(assigned, chosen)
	} else {
		for _, hostID := range p.HostIDs {
			busy, err := hostBusy(ctx, tx, hostID, startStr, endStr, "")
			if err != nil {
				return nil, fmt.Errorf("booking: overlap check: %w", err)
			}
			if busy {
				return nil, ErrDoubleBooked
			}
		}
		chosenHost = p.HostIDs[0]
		assigned = append(assigned, p.HostIDs...)
	}

	// Optional hosts attend only if free; a busy one is simply left off.
	for _, hostID := range p.OptionalHosts {
		if slices.Contains(assigned, hostID) {
			continue
		}
		busy, err := hostBusy(ctx, tx, hostID, startStr, endStr, "")
		if err != nil {
			return nil, fmt.Errorf("booking: optional overlap check: %w", err)
		}
		if !busy {
			assigned = append(assigned, hostID)
		}
	}

	// Enforce the per-invitee active-booking cap (0 = unlimited). "Active" means a
	// non-cancelled booking for this event type, held by the same email, that has
	// not yet ended — past bookings don't count. Checked inside the transaction so
	// two simultaneous submissions by the same invitee can't both slip past.
	if p.MaxActivePerInvitee > 0 {
		var active int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM bookings b
			JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
			WHERE b.event_type_id = ? AND b.status != 'cancelled'
			  AND b.end_at > ? AND a.email = ? COLLATE NOCASE`,
			p.EventTypeID, now, p.Organizer.Email).Scan(&active); err != nil {
			return nil, fmt.Errorf("booking: active-limit check: %w", err)
		}
		if active >= p.MaxActivePerInvitee {
			return nil, ErrBookingLimitReached
		}
	}

	bookingID := uid.New()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO bookings
		  (id, event_type_id, host_id, start_at, end_at, status, location_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'confirmed', ?, ?, ?)`,
		bookingID, p.EventTypeID, chosenHost, startStr, endStr, p.LocationValue, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDoubleBooked
		}
		return nil, fmt.Errorf("booking: insert: %w", err)
	}

	// Record every attending host; the primary mirrors bookings.host_id.
	for _, hostID := range assigned {
		isPrimary := 0
		if hostID == chosenHost {
			isPrimary = 1
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO booking_hosts (id, booking_id, user_id, is_primary)
			VALUES (?, ?, ?, ?)`,
			uid.New(), bookingID, hostID, isPrimary); err != nil {
			return nil, fmt.Errorf("booking: insert host: %w", err)
		}
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
		HostID:        chosenHost,
		StartAt:       p.StartAt.UTC(),
		EndAt:         p.EndAt.UTC(),
		Status:        "confirmed",
		LocationValue: p.LocationValue,
		CreatedAt:     nowT,
		UpdatedAt:     nowT,
	}, nil
}

// Cancel marks a booking as cancelled. hostID must match the booking's host_id
// so that one user cannot cancel another user's bookings. Returns ErrNotFound
// if the booking does not exist or belongs to a different host, and
// ErrAlreadyCancelled if it is already in that state.
func (s *Service) Cancel(ctx context.Context, hostID, id, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE bookings
		SET status = 'cancelled', cancellation_reason = ?, updated_at = ?
		WHERE id = ? AND host_id = ? AND status != 'cancelled'`,
		reason, now, id, hostID)
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
			`SELECT COUNT(*) FROM bookings WHERE id = ? AND host_id = ?`, id, hostID).Scan(&exists); err != nil {
			return fmt.Errorf("booking: cancel existence check: %w", err)
		}
		if exists == 0 {
			return ErrNotFound
		}
		return ErrAlreadyCancelled
	}
	return nil
}

// CancelByID cancels a booking regardless of host — for admin actions such as
// resolving a departing member's meetings during archiving. Ownership is
// enforced by the caller (admin check in the handler), not here.
func (s *Service) CancelByID(ctx context.Context, id, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE bookings
		SET status = 'cancelled', cancellation_reason = ?, updated_at = ?
		WHERE id = ? AND status != 'cancelled'`,
		reason, now, id)
	if err != nil {
		return fmt.Errorf("booking: cancel by id: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM bookings WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("booking: cancel by id existence check: %w", err)
		}
		if exists == 0 {
			return ErrNotFound
		}
		return ErrAlreadyCancelled
	}
	return nil
}

// bookingColumns is the column list scanBooking expects, in order — shared by every
// query that loads a full Booking, so a schema change (a column added/removed) is
// one edit instead of five.
const bookingColumns = `id, event_type_id, host_id, start_at, end_at, status,
	       COALESCE(cancellation_reason, ''), COALESCE(location_value, ''),
	       created_at, updated_at,
	       payment_status, amount_paid_cents, amount_paid_currency`

// hostBusy reports whether hostID has any non-cancelled booking overlapping
// [start, end) — the double-booking invariant every write path (Create, Reschedule,
// ReassignHost) must check before committing a time change. A host is busy if they
// attend ANY overlapping booking, primary or not, so this joins booking_hosts rather
// than matching bookings.host_id (which would miss a Group/fixed-host attendee).
// excludeBookingID excludes the booking being modified from its own overlap check
// (Reschedule/ReassignHost); pass "" when there's no booking yet to exclude (Create).
func hostBusy(ctx context.Context, tx *sql.Tx, hostID, start, end, excludeBookingID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM bookings b
		JOIN booking_hosts bh ON bh.booking_id = b.id
		WHERE bh.user_id = ? AND b.status != 'cancelled' AND b.id != ?
		  AND b.start_at < ? AND b.end_at > ?`,
		hostID, excludeBookingID, end, start).Scan(&n)
	return n > 0, err
}

// Get returns a single booking by ID.
func (s *Service) Get(ctx context.Context, id string) (*Booking, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+bookingColumns+` FROM bookings WHERE id = ?`, id)
	return scanBooking(row)
}

// ListByHost returns all non-cancelled bookings a user is hosting, ordered by
// start time. A user "hosts" a booking if they are the primary host (host_id) OR
// any assigned host in booking_hosts — so Group attendees (required or optional,
// not just the primary) see meetings they're on, not only the ones they lead.
func (s *Service) ListByHost(ctx context.Context, hostID string) ([]Booking, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+bookingColumns+`
		FROM bookings
		WHERE status != 'cancelled'
		  AND (host_id = ? OR EXISTS (
		        SELECT 1 FROM booking_hosts bh
		        WHERE bh.booking_id = bookings.id AND bh.user_id = ?))
		ORDER BY start_at`, hostID, hostID)
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

// ListAll returns every non-cancelled booking in the workspace, ordered by start
// time (matching ListByHost). For the admin/owner "All bookings" view — callers
// must gate this on the admin role.
func (s *Service) ListAll(ctx context.Context) ([]Booking, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+bookingColumns+`
		FROM bookings
		WHERE status != 'cancelled'
		ORDER BY start_at`)
	if err != nil {
		return nil, fmt.Errorf("booking: list all: %w", err)
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

// IssueManageToken generates a cryptographically random manage token for a
// booking, stores its SHA-256 hash in booking_manage_tokens, and returns the
// raw hex token (shown once, embedded in emails). Tokens expire in 60 days.
func (s *Service) IssueManageToken(ctx context.Context, bookingID string) (string, error) {
	rawHex, hash, expiresAt, err := generateManageToken()
	if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO booking_manage_tokens (token_hash, booking_id, expires_at)
		VALUES (?, ?, ?)`, hash, bookingID, expiresAt); err != nil {
		return "", fmt.Errorf("booking: insert manage token: %w", err)
	}
	return rawHex, nil
}

// manageTokenTTL is how long an issued/rotated manage-link token stays valid.
const manageTokenTTL = 60 * 24 * time.Hour

// generateManageToken produces a new manage-token: a random 32-byte value (returned
// hex-encoded as rawHex, given to the recipient in their manage link) and its
// SHA-256 hash (hash, the only form stored in the DB) plus its expiry. Pure and
// side-effect-free — callers do their own DB write with the returned values, so
// IssueManageToken and RotateManageToken share exactly one implementation of the
// token format/TTL instead of two.
func generateManageToken() (rawHex, hash, expiresAt string, err error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", "", "", fmt.Errorf("booking: generate token: %w", err)
	}
	rawHex = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash = hex.EncodeToString(sum[:])
	expiresAt = time.Now().UTC().Add(manageTokenTTL).Format(time.RFC3339)
	return rawHex, hash, expiresAt, nil
}

// ValidateManageToken looks up a manage token by its hash and returns the
// associated booking. Returns ErrTokenNotFound if the token is missing or
// expired.
func (s *Service) ValidateManageToken(ctx context.Context, rawToken string) (*Booking, error) {
	sum := sha256.Sum256([]byte(rawToken))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339)

	var bookingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT booking_id FROM booking_manage_tokens
		WHERE token_hash = ? AND expires_at > ?`, hash, now).Scan(&bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("booking: validate token: %w", err)
	}
	return s.Get(ctx, bookingID)
}

// pickRotationHost chooses one free rotation host per strategy. free must already
// be in priority order (lowest priority number first). "priority" takes the first
// free host; "even" (and "soonest", which has no meaning once the slot is fixed)
// take the least-loaded one.
func pickRotationHost(ctx context.Context, tx *sql.Tx, eventTypeID, strategy string, free []string, now string) (string, error) {
	if strategy == "priority" {
		return free[0], nil
	}
	return leastLoadedHost(ctx, tx, eventTypeID, free, now)
}

// leastLoadedHost returns the candidate with the fewest upcoming (non-cancelled,
// not-yet-ended) bookings for this event type — even-distribution round-robin.
// Ties are broken by the order of candidates (caller passes them in priority order).
func leastLoadedHost(ctx context.Context, tx *sql.Tx, eventTypeID string, candidates []string, now string) (string, error) {
	ph := make([]string, len(candidates))
	args := make([]any, 0, len(candidates)+2)
	args = append(args, eventTypeID, now)
	for i, c := range candidates {
		ph[i] = "?"
		args = append(args, c)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT host_id, COUNT(*) FROM bookings
		WHERE event_type_id = ? AND status != 'cancelled' AND end_at > ?
		  AND host_id IN (`+strings.Join(ph, ",")+`)
		GROUP BY host_id`, args...)
	if err != nil {
		return "", fmt.Errorf("booking: load host loads: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var hid string
		var n int
		if err := rows.Scan(&hid, &n); err != nil {
			return "", err
		}
		counts[hid] = n
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	best := candidates[0]
	bestN := counts[best]
	for _, c := range candidates[1:] {
		if counts[c] < bestN {
			bestN = counts[c]
			best = c
		}
	}
	return best, nil
}

// Reschedule moves a booking to a new start/end time inside a transaction.
// Returns ErrNotFound if the booking doesn't exist, ErrAlreadyCancelled if
// it is cancelled, and ErrDoubleBooked if the new slot overlaps another
// confirmed booking for the same host.
func (s *Service) Reschedule(ctx context.Context, bookingID string, newStart, newEnd time.Time) (*Booking, error) {
	startStr := newStart.UTC().Format(time.RFC3339Nano)
	endStr := newEnd.UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("booking: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b, err := scanBooking(tx.QueryRowContext(ctx, `SELECT `+bookingColumns+` FROM bookings WHERE id = ?`, bookingID))
	if err != nil {
		return nil, err
	}
	if b.Status == "cancelled" {
		return nil, ErrAlreadyCancelled
	}

	// Every host on this booking keeps their seat through a reschedule, so each
	// must be free at the new time — not just the primary. Read the host list
	// fully before the per-host overlap queries (single-connection pool).
	hostRows, err := tx.QueryContext(ctx, `SELECT user_id FROM booking_hosts WHERE booking_id = ?`, bookingID)
	if err != nil {
		return nil, fmt.Errorf("booking: reschedule hosts: %w", err)
	}
	var hostIDs []string
	for hostRows.Next() {
		var u string
		if err := hostRows.Scan(&u); err != nil {
			hostRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, fmt.Errorf("booking: reschedule hosts scan: %w", err)
		}
		hostIDs = append(hostIDs, u)
	}
	hostRows.Close()       // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if len(hostIDs) == 0 { // legacy booking with no booking_hosts rows
		hostIDs = []string{b.HostID}
	}
	for _, hid := range hostIDs {
		busy, err := hostBusy(ctx, tx, hid, startStr, endStr, bookingID)
		if err != nil {
			return nil, fmt.Errorf("booking: reschedule overlap: %w", err)
		}
		if busy {
			return nil, ErrDoubleBooked
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE bookings SET start_at = ?, end_at = ?, updated_at = ? WHERE id = ?`,
		startStr, endStr, now, bookingID); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDoubleBooked
		}
		return nil, fmt.Errorf("booking: reschedule update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("booking: reschedule commit: %w", err)
	}

	b.StartAt = newStart.UTC()
	b.EndAt = newEnd.UTC()
	if t, err := time.Parse(time.RFC3339Nano, now); err == nil {
		b.UpdatedAt = t
	}
	return b, nil
}

// ReassignHost moves a booking to a different host inside a transaction,
// checking the new host is free at the booking's time. Returns ErrNotFound if
// the booking doesn't exist, ErrAlreadyCancelled if it is cancelled, and
// ErrDoubleBooked if the new host already has an overlapping confirmed booking.
// Reassigning to the current host is a no-op that returns the booking unchanged.
func (s *Service) ReassignHost(ctx context.Context, bookingID, newHostID string) (*Booking, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("booking: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b, err := scanBooking(tx.QueryRowContext(ctx, `SELECT `+bookingColumns+` FROM bookings WHERE id = ?`, bookingID))
	if err != nil {
		return nil, err
	}
	if b.Status == "cancelled" {
		return nil, ErrAlreadyCancelled
	}
	if b.HostID == newHostID {
		return b, nil // already this host — nothing to do
	}

	startStr := b.StartAt.UTC().Format(time.RFC3339Nano)
	endStr := b.EndAt.UTC().Format(time.RFC3339Nano)

	// The new host must be free at this time across everything they attend.
	busy, err := hostBusy(ctx, tx, newHostID, startStr, endStr, bookingID)
	if err != nil {
		return nil, fmt.Errorf("booking: reassign overlap: %w", err)
	}
	if busy {
		return nil, ErrDoubleBooked
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE bookings SET host_id = ?, updated_at = ? WHERE id = ?`,
		newHostID, now, bookingID); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDoubleBooked
		}
		return nil, fmt.Errorf("booking: reassign update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("booking: reassign commit: %w", err)
	}

	b.HostID = newHostID
	if t, err := time.Parse(time.RFC3339Nano, now); err == nil {
		b.UpdatedAt = t
	}
	return b, nil
}

// CancelByToken cancels a booking authenticated by a manage token.
// Unlike Cancel, it does not require host authentication.
func (s *Service) CancelByToken(ctx context.Context, rawToken, reason string) (*Booking, error) {
	b, err := s.ValidateManageToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	if b.Status == "cancelled" {
		return nil, ErrAlreadyCancelled
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE bookings SET status = 'cancelled', cancellation_reason = ?, updated_at = ?
		WHERE id = ? AND status != 'cancelled'`,
		reason, now, b.ID)
	if err != nil {
		return nil, fmt.Errorf("booking: cancel by token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// A concurrent cancel won the race between our status check and this UPDATE.
		return nil, ErrAlreadyCancelled
	}
	b.Status = "cancelled"
	b.CancellationReason = reason
	if t, err := time.Parse(time.RFC3339Nano, now); err == nil {
		b.UpdatedAt = t
	}
	return b, nil
}

// RotateManageToken invalidates all existing manage tokens for a booking and
// issues a fresh one atomically. Called after a reschedule so that the original
// confirmation-email link cannot be reused or undo the new time.
func (s *Service) RotateManageToken(ctx context.Context, bookingID string) (string, error) {
	rawHex, hash, expiresAt, err := generateManageToken()
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("booking: rotate token begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM booking_manage_tokens WHERE booking_id = ?`, bookingID); err != nil {
		return "", fmt.Errorf("booking: delete old tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO booking_manage_tokens (token_hash, booking_id, expires_at)
		VALUES (?, ?, ?)`, hash, bookingID, expiresAt); err != nil {
		return "", fmt.Errorf("booking: insert rotated token: %w", err)
	}
	return rawHex, tx.Commit()
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
		&b.PaymentStatus, &b.AmountPaidCents, &b.AmountPaidCurrency,
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
