package worker_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

func setup(t *testing.T) (*sql.DB, *webhook.Service) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Insert users so FK constraints are satisfied.
	ctx := context.Background()
	for _, u := range []struct{ id, email, name string }{
		{"host-01", "host01@example.com", "Host One"},
		{"host-02", "host02@example.com", "Host Two"},
		{"host-03", "host03@example.com", "Host Three"},
		{"host-04", "host04@example.com", "Host Four"},
	} {
		database.ExecContext(ctx,
			`INSERT INTO users (id, email, name) VALUES (?, ?, ?)`, u.id, u.email, u.name)
	}

	svc, err := webhook.New(database, "")
	if err != nil {
		t.Fatalf("webhook.New: %v", err)
	}
	return database, svc
}

func newWorker(t *testing.T, database *sql.DB, svc *webhook.Service) *worker.Worker {
	t.Helper()
	// Tests use a local httptest.Server, so bypass the production SSRF guard.
	return worker.New(database, svc, slog.Default(),
		worker.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
}

// ---------------------------------------------------------------------------
// Successful delivery
// ---------------------------------------------------------------------------

func TestWorker_deliversSuccessfully(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	var received struct {
		headers http.Header
		body    []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.headers = r.Header.Clone()
		received.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh, plainSecret, _ := svc.Create(ctx, "host-01", srv.URL, []string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: "host-01",
		Status: "confirmed",
	})

	w := newWorker(t, database, svc)
	w.Poll(ctx)

	if received.body == nil {
		t.Fatal("worker never POSTed to webhook URL")
	}
	if ct := received.headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	if ev := received.headers.Get("X-Calnode-Event"); ev != "booking.created" {
		t.Errorf("X-Calnode-Event = %q; want booking.created", ev)
	}
	if received.headers.Get("X-Calnode-Delivery") == "" {
		t.Error("X-Calnode-Delivery header missing")
	}

	// Verify HMAC signature.
	sig := received.headers.Get("X-Calnode-Signature")
	secretBytes, _ := hex.DecodeString(plainSecret)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write(received.body)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig != wantSig {
		t.Errorf("X-Calnode-Signature = %q; want %q", sig, wantSig)
	}

	// Verify payload envelope.
	var envelope map[string]any
	if err := json.Unmarshal(received.body, &envelope); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if envelope["event"] != "booking.created" {
		t.Errorf("envelope event = %v; want booking.created", envelope["event"])
	}

	// Delivery → success.
	var status string
	database.QueryRowContext(ctx,
		`SELECT status FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).Scan(&status)
	if status != "success" {
		t.Errorf("delivery status = %q; want success", status)
	}

	// Job → done.
	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs WHERE type = 'webhook.deliver'`).
		Scan(&jobStatus)
	if jobStatus != "done" {
		t.Errorf("job status = %q; want done", jobStatus)
	}
}

// ---------------------------------------------------------------------------
// Endpoint returns 5xx — retry until exhausted
// ---------------------------------------------------------------------------

func TestWorker_marksJobFailedAfterExhaustingRetries(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh, _, _ := svc.Create(ctx, "host-02", srv.URL, []string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: "host-02",
		Status: "confirmed",
	})

	w := newWorker(t, database, svc)
	past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)

	for range 3 {
		database.ExecContext(ctx,
			`UPDATE jobs SET run_at = ?, status = 'pending'
			 WHERE type = 'webhook.deliver' AND status IN ('pending','running','failed')`,
			past)
		w.Poll(ctx)
	}

	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs WHERE type = 'webhook.deliver'`).
		Scan(&jobStatus)
	if jobStatus != "failed" {
		t.Errorf("job status = %q; want failed after 3 attempts", jobStatus)
	}

	var delivStatus string
	database.QueryRowContext(ctx,
		`SELECT status FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).Scan(&delivStatus)
	if delivStatus != "failed" {
		t.Errorf("delivery status = %q; want failed", delivStatus)
	}
}

// ---------------------------------------------------------------------------
// Backoff — second poll must not fire immediately after first failure
// ---------------------------------------------------------------------------

func TestWorker_respectsBackoff(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc.Create(ctx, "host-03", srv.URL, []string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: "host-03",
		Status: "confirmed",
	})

	w := newWorker(t, database, svc)

	w.Poll(ctx)
	if callCount != 1 {
		t.Fatalf("call count = %d after attempt 1; want 1", callCount)
	}

	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&jobStatus)
	if jobStatus != "pending" {
		t.Errorf("job status = %q; want pending (awaiting backoff)", jobStatus)
	}

	// Immediate re-poll must not fire (run_at is in the future).
	w.Poll(ctx)
	if callCount != 1 {
		t.Errorf("call count = %d after premature re-poll; want 1 (backoff)", callCount)
	}
}

// ---------------------------------------------------------------------------
// Deleted webhook — job is silently completed
// ---------------------------------------------------------------------------

func TestWorker_skipsJobForDeletedWebhook(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	wh, _, _ := svc.Create(ctx, "host-04", "https://example.com/hook",
		[]string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		HostID: "host-04",
		Status: "confirmed",
	})

	svc.Delete(ctx, "host-04", wh.ID)

	w := newWorker(t, database, svc)
	w.Poll(ctx) // must not panic

	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&jobStatus)
	if jobStatus != "done" {
		t.Errorf("job status = %q; want done (skipped silently)", jobStatus)
	}
}
