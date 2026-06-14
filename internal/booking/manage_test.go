package booking_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
)

func TestIssueManageToken_returnsHexToken(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tok, err := svc.IssueManageToken(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("IssueManageToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length = %d; want 64 hex chars", len(tok))
	}
	for _, c := range tok {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("token contains non-hex char %q", c)
			break
		}
	}
}

func TestIssueManageToken_multipleTokensAllValid(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tok1, err := svc.IssueManageToken(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("IssueManageToken 1: %v", err)
	}
	tok2, err := svc.IssueManageToken(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("IssueManageToken 2: %v", err)
	}
	if tok1 == tok2 {
		t.Error("expected two different tokens, got the same value")
	}

	// Both tokens must resolve to the same booking.
	got1, err := svc.ValidateManageToken(context.Background(), tok1)
	if err != nil {
		t.Fatalf("ValidateManageToken(tok1): %v", err)
	}
	got2, err := svc.ValidateManageToken(context.Background(), tok2)
	if err != nil {
		t.Fatalf("ValidateManageToken(tok2): %v", err)
	}
	if got1.ID != b.ID || got2.ID != b.ID {
		t.Errorf("expected booking ID %q for both tokens; got %q and %q", b.ID, got1.ID, got2.ID)
	}
}

func TestValidateManageToken_validToken(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tok, err := svc.IssueManageToken(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("IssueManageToken: %v", err)
	}

	got, err := svc.ValidateManageToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("ValidateManageToken: %v", err)
	}
	if got.ID != b.ID {
		t.Errorf("booking ID = %q; want %q", got.ID, b.ID)
	}
	if got.Status != "confirmed" {
		t.Errorf("Status = %q; want confirmed", got.Status)
	}
}

func TestValidateManageToken_invalidToken(t *testing.T) {
	svc := booking.New(newTestDB(t))

	_, err := svc.ValidateManageToken(context.Background(), "not-a-real-token")
	if err != booking.ErrTokenNotFound {
		t.Errorf("ValidateManageToken(invalid): got %v; want ErrTokenNotFound", err)
	}
}

func TestValidateManageToken_expiredToken(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Insert a token that expired in the past.
	rawToken := "expired-token-raw-value-for-test-only"
	sum := sha256.Sum256([]byte(rawToken))
	hash := hex.EncodeToString(sum[:])
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := database.ExecContext(context.Background(), `
		INSERT INTO booking_manage_tokens (token_hash, booking_id, expires_at)
		VALUES (?, ?, ?)`, hash, b.ID, past); err != nil {
		t.Fatalf("insert expired token: %v", err)
	}

	_, err = svc.ValidateManageToken(context.Background(), rawToken)
	if err != booking.ErrTokenNotFound {
		t.Errorf("ValidateManageToken(expired): got %v; want ErrTokenNotFound", err)
	}
}

func TestReschedule_success(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newStart := slot(10, 0)
	newEnd := slot(10, 30)
	updated, err := svc.Reschedule(context.Background(), b.ID, newStart, newEnd)
	if err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if !updated.StartAt.Equal(newStart) {
		t.Errorf("StartAt = %v; want %v", updated.StartAt, newStart)
	}
	if !updated.EndAt.Equal(newEnd) {
		t.Errorf("EndAt = %v; want %v", updated.EndAt, newEnd)
	}
	if updated.Status != "confirmed" {
		t.Errorf("Status = %q; want confirmed", updated.Status)
	}

	// Verify persisted.
	got, err := svc.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("Get after Reschedule: %v", err)
	}
	if !got.StartAt.Equal(newStart) {
		t.Errorf("persisted StartAt = %v; want %v", got.StartAt, newStart)
	}
}

func TestReschedule_sameTime(t *testing.T) {
	// Rescheduling to the same time must succeed (self-exclusion in overlap check).
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Reschedule(context.Background(), b.ID, slot(9, 0), slot(9, 30))
	if err != nil {
		t.Errorf("Reschedule to same time: got %v; want nil", err)
	}
}

func TestReschedule_doubleBooked(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	// Occupy 10:00-10:30.
	if _, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(10, 0),
		EndAt:       slot(10, 30),
		Organizer:   booking.Attendee{Name: "Bob", Email: "bob@example.com"},
	}); err != nil {
		t.Fatalf("seed conflicting booking: %v", err)
	}

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Try to reschedule to 10:00-10:30 which is already taken.
	_, err = svc.Reschedule(context.Background(), b.ID, slot(10, 0), slot(10, 30))
	if err != booking.ErrDoubleBooked {
		t.Errorf("Reschedule to conflicting slot: got %v; want ErrDoubleBooked", err)
	}
}

func TestReschedule_alreadyCancelled(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Cancel(context.Background(), hostID, b.ID, "gone"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	_, err = svc.Reschedule(context.Background(), b.ID, slot(11, 0), slot(11, 30))
	if err != booking.ErrAlreadyCancelled {
		t.Errorf("Reschedule of cancelled booking: got %v; want ErrAlreadyCancelled", err)
	}
}

func TestCancelByToken_success(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tok, err := svc.IssueManageToken(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("IssueManageToken: %v", err)
	}

	cancelled, err := svc.CancelByToken(context.Background(), tok, "changed plans")
	if err != nil {
		t.Fatalf("CancelByToken: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("Status = %q; want cancelled", cancelled.Status)
	}
	if cancelled.CancellationReason != "changed plans" {
		t.Errorf("CancellationReason = %q; want %q", cancelled.CancellationReason, "changed plans")
	}

	// Verify persisted.
	got, err := svc.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("Get after CancelByToken: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("persisted Status = %q; want cancelled", got.Status)
	}
}

func TestCancelByToken_invalidToken(t *testing.T) {
	svc := booking.New(newTestDB(t))
	_, err := svc.CancelByToken(context.Background(), "bogus-token", "")
	if err != booking.ErrTokenNotFound {
		t.Errorf("CancelByToken(invalid): got %v; want ErrTokenNotFound", err)
	}
}

func TestCancelByToken_alreadyCancelled(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Cancel(context.Background(), hostID, b.ID, "first"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	tok, err := svc.IssueManageToken(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("IssueManageToken: %v", err)
	}

	_, err = svc.CancelByToken(context.Background(), tok, "second")
	if err != booking.ErrAlreadyCancelled {
		t.Errorf("CancelByToken on already-cancelled: got %v; want ErrAlreadyCancelled", err)
	}
}
