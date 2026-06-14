package webhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/webhook"
)

const (
	testUserID = "user-test-01"
	otherUser  = "user-test-02"
)

type env struct {
	svc *webhook.Service
	db  *sql.DB
}

func newEnv(t *testing.T) *env {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ctx := context.Background()
	for _, u := range []struct{ id, email, name string }{
		{testUserID, "test@example.com", "Test User"},
		{otherUser, "other@example.com", "Other User"},
	} {
		database.ExecContext(ctx,
			`INSERT INTO users (id, email, name) VALUES (?, ?, ?)`, u.id, u.email, u.name)
	}

	svc, err := webhook.New(database, "")
	if err != nil {
		t.Fatalf("webhook.New: %v", err)
	}
	return &env{svc: svc, db: database}
}

// ---------------------------------------------------------------------------
// Encryption key validation
// ---------------------------------------------------------------------------

func TestNew_badKeyReturnsError(t *testing.T) {
	database, _ := db.Open("sqlite://:memory:")
	db.Migrate(database)
	defer database.Close()

	if _, err := webhook.New(database, "not-hex"); err == nil {
		t.Error("expected error for non-hex key")
	}
	if _, err := webhook.New(database, "010203"); err == nil {
		t.Error("expected error for key that is too short")
	}
}

func TestNew_emptyKeyUsesEphemeral(t *testing.T) {
	database, _ := db.Open("sqlite://:memory:")
	db.Migrate(database)
	defer database.Close()

	svc, err := webhook.New(database, "")
	if err != nil || svc == nil {
		t.Fatalf("unexpected error with empty key: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Create / List / Delete
// ---------------------------------------------------------------------------

func TestCreate_returnsWebhookAndSecret(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, secret, err := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wh.ID == "" {
		t.Error("expected non-empty ID")
	}
	if wh.URL != "https://example.com/hook" {
		t.Errorf("URL = %q", wh.URL)
	}
	if len(wh.Events) != 1 || wh.Events[0] != "booking.created" {
		t.Errorf("Events = %v", wh.Events)
	}
	if !wh.IsActive {
		t.Error("expected IsActive = true")
	}
	if len(secret) != 64 {
		t.Errorf("secret length = %d; want 64 hex chars", len(secret))
	}
}

func TestCreate_secretIsDecodableHex(t *testing.T) {
	e := newEnv(t)
	_, secret, err := e.svc.Create(context.Background(), testUserID, "https://example.com/hook",
		[]string{"booking.created"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b, err := hex.DecodeString(secret)
	if err != nil {
		t.Fatalf("secret is not valid hex: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("decoded secret = %d bytes; want 32", len(b))
	}
}

func TestList_returnsCreatedWebhooks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	items, _ := e.svc.List(ctx, testUserID)
	if len(items) != 0 {
		t.Errorf("initial list len = %d; want 0", len(items))
	}

	e.svc.Create(ctx, testUserID, "https://a.example.com/hook", []string{"booking.created"})
	e.svc.Create(ctx, testUserID, "https://b.example.com/hook", []string{"booking.cancelled"})
	e.svc.Create(ctx, otherUser, "https://c.example.com/hook", []string{"booking.created"})

	items, err := e.svc.List(ctx, testUserID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("list len = %d; want 2", len(items))
	}
}

func TestDelete_removesOwnWebhook(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})
	if err := e.svc.Delete(ctx, testUserID, wh.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	items, _ := e.svc.List(ctx, testUserID)
	if len(items) != 0 {
		t.Error("webhook should be gone after delete")
	}
}

func TestDelete_cannotDeleteOtherUsersWebhook(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})

	if err := e.svc.Delete(ctx, otherUser, wh.ID); err == nil {
		t.Fatal("expected error deleting another user's webhook")
	}
}

func TestDelete_unknownIDReturnsNotFound(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Delete(context.Background(), testUserID, "no-such-id"); err == nil {
		t.Fatal("expected ErrNotFound for unknown webhook")
	}
}

// ---------------------------------------------------------------------------
// Enqueue
// ---------------------------------------------------------------------------

func TestEnqueue_createsDeliveryAndJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})

	p := webhook.BookingPayload{
		EventTypeSlug: "30-min",
		HostID:        testUserID,
		StartAt:       "2026-06-15T09:00:00Z",
		EndAt:         "2026-06-15T09:30:00Z",
		Status:        "confirmed",
		CreatedAt:     "2026-06-14T15:00:00Z",
	}
	if err := e.svc.Enqueue(ctx, "booking.created", p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var deliveryCount int
	e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).
		Scan(&deliveryCount)
	if deliveryCount != 1 {
		t.Errorf("delivery count = %d; want 1", deliveryCount)
	}

	var jobCount int
	e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE type = 'webhook.deliver' AND status = 'pending'`).
		Scan(&jobCount)
	if jobCount != 1 {
		t.Errorf("job count = %d; want 1", jobCount)
	}
}

func TestEnqueue_doesNotFireForNonSubscribedEvent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.svc.Create(ctx, testUserID, "https://example.com/hook", []string{"booking.created"})

	p := webhook.BookingPayload{
		HostID: testUserID,
		Status: "cancelled",
	}
	if err := e.svc.Enqueue(ctx, "booking.cancelled", p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var count int
	e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count)
	if count != 0 {
		t.Errorf("delivery count = %d; want 0 (wrong event subscribed)", count)
	}
}

func TestEnqueue_doesNotFireForDifferentHost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.svc.Create(ctx, testUserID, "https://example.com/hook", []string{"booking.created"})

	p := webhook.BookingPayload{
		HostID: otherUser, // booking belongs to a different host
		Status: "confirmed",
	}
	if err := e.svc.Enqueue(ctx, "booking.created", p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var count int
	e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count)
	if count != 0 {
		t.Errorf("delivery count = %d; want 0 (wrong host)", count)
	}
}

func TestEnqueue_firesAllMatchingWebhooks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	e.svc.Create(ctx, testUserID, "https://a.example.com/hook", []string{"booking.created"})
	e.svc.Create(ctx, testUserID, "https://b.example.com/hook", []string{"booking.created", "booking.cancelled"})

	p := webhook.BookingPayload{
		HostID: testUserID,
		Status: "confirmed",
	}
	if err := e.svc.Enqueue(ctx, "booking.created", p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var count int
	e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count)
	if count != 2 {
		t.Errorf("delivery count = %d; want 2 (both webhooks match)", count)
	}
}

// ---------------------------------------------------------------------------
// ListDeliveries
// ---------------------------------------------------------------------------

func TestListDeliveries_requiresOwnership(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})

	if _, err := e.svc.ListDeliveries(ctx, otherUser, wh.ID); err == nil {
		t.Error("expected error listing deliveries as non-owner")
	}
}

func TestListDeliveries_returnsEmptyInitially(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})

	deliveries, err := e.svc.ListDeliveries(ctx, testUserID, wh.ID)
	if err != nil {
		t.Fatalf("ListDeliveries: %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("len = %d; want 0", len(deliveries))
	}
}

func TestListDeliveries_showsPendingAfterEnqueue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})
	e.svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: testUserID, Status: "confirmed",
	})

	deliveries, err := e.svc.ListDeliveries(ctx, testUserID, wh.ID)
	if err != nil {
		t.Fatalf("ListDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("len = %d; want 1", len(deliveries))
	}
	if deliveries[0].Status != "pending" {
		t.Errorf("status = %q; want pending", deliveries[0].Status)
	}
	if deliveries[0].Event != "booking.created" {
		t.Errorf("event = %q; want booking.created", deliveries[0].Event)
	}
}

// ---------------------------------------------------------------------------
// Signing
// ---------------------------------------------------------------------------

func TestSign_matchesManualHMAC(t *testing.T) {
	secret := []byte("test-secret-for-unit-test-only")
	payload := []byte(`{"event":"booking.created"}`)

	got := webhook.Sign(secret, payload)

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Errorf("Sign = %q; want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// DecryptSecret round-trip
// ---------------------------------------------------------------------------

func TestDecryptSecret_roundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	wh, plainSecret, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.created"})

	var encSecret string
	e.db.QueryRowContext(ctx,
		`SELECT secret_enc FROM webhooks WHERE id = ?`, wh.ID).Scan(&encSecret)

	decrypted, err := e.svc.DecryptSecret(encSecret)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if hex.EncodeToString(decrypted) != plainSecret {
		t.Error("decrypted secret does not match the plain secret shown at creation")
	}
}
