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
// Expired manage-token cleanup
// ---------------------------------------------------------------------------

func TestWorker_purgesExpiredManageTokens(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	// Seed minimal records to satisfy FK constraints (PRAGMA foreign_keys=ON).
	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-purge','host-01','purge-test','Purge Test',30)`)
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at)
		 VALUES ('bk-purge','et-purge','host-01','2026-06-14T09:00:00Z','2026-06-14T09:30:00Z')`)

	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)

	database.ExecContext(ctx,
		`INSERT INTO booking_manage_tokens (token_hash, booking_id, expires_at) VALUES ('dead','bk-purge',?)`, past)
	database.ExecContext(ctx,
		`INSERT INTO booking_manage_tokens (token_hash, booking_id, expires_at) VALUES ('live','bk-purge',?)`, future)

	w := newWorker(t, database, svc)
	w.Poll(ctx)

	var count int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM booking_manage_tokens`).Scan(&count)
	if count != 1 {
		t.Errorf("token count = %d; want 1 (expired token should be purged)", count)
	}
	var remaining string
	database.QueryRowContext(ctx, `SELECT token_hash FROM booking_manage_tokens`).Scan(&remaining)
	if remaining != "live" {
		t.Errorf("remaining token = %q; want live", remaining)
	}
}

// ---------------------------------------------------------------------------
// Stuck-job reaper
// ---------------------------------------------------------------------------

func TestWorker_reaperRecoversCrashedJob(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc.Create(ctx, "host-01", srv.URL, []string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: "host-01", Status: "confirmed"})

	// Simulate a crash: manually force the job into 'running' with an expired lock.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	database.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', locked_until = ? WHERE status = 'pending'`, past)

	var stuckStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&stuckStatus)
	if stuckStatus != "running" {
		t.Fatalf("precondition: job should be running, got %q", stuckStatus)
	}

	w := newWorker(t, database, svc)

	// First Poll: reaper resets the expired job to 'pending' with a future run_at
	// (to avoid an immediate same-cycle retry). The job is NOT processed yet.
	w.Poll(ctx)

	var midStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&midStatus)
	if midStatus != "pending" {
		t.Fatalf("after first poll: job status = %q; want pending (reaped but delayed)", midStatus)
	}
	if callCount != 0 {
		t.Errorf("after first poll: call count = %d; want 0 (not yet processed)", callCount)
	}

	// Advance run_at to the past so the next Poll picks it up.
	past2 := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	database.ExecContext(ctx, `UPDATE jobs SET run_at = ? WHERE status = 'pending'`, past2)

	// Second Poll: picks up and delivers the job.
	w.Poll(ctx)

	var finalStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&finalStatus)
	if finalStatus != "done" {
		t.Errorf("after second poll: job status = %q; want done", finalStatus)
	}
	if callCount != 1 {
		t.Errorf("call count = %d; want 1 (webhook delivered after recovery)", callCount)
	}
}

func TestWorker_reaperMarksFailedWhenAttemptsExhausted(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	svc.Create(ctx, "host-02", "https://example.com/hook", []string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: "host-02", Status: "confirmed"})

	// Simulate a job that crashed after exhausting all 3 attempts.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	database.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', locked_until = ?, attempts = 3, max_attempts = 3
		 WHERE status = 'pending'`, past)

	w := newWorker(t, database, svc)
	w.Poll(ctx)

	var status string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&status)
	if status != "failed" {
		t.Errorf("job status = %q; want failed (exhausted attempts should not be reaped to pending)", status)
	}
}

func TestWorker_reaperIgnoresActiveLocks(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	svc.Create(ctx, "host-02", "https://example.com/hook", []string{"booking.created"})
	svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: "host-02", Status: "confirmed"})

	// Simulate an in-flight job: locked_until is in the future.
	future := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	database.ExecContext(ctx,
		`UPDATE jobs SET status = 'running', locked_until = ? WHERE status = 'pending'`, future)

	w := newWorker(t, database, svc)
	w.Poll(ctx)

	// Job must still be running — reaper must not have touched it.
	var status string
	database.QueryRowContext(ctx, `SELECT status FROM jobs`).Scan(&status)
	if status != "running" {
		t.Errorf("job status = %q; want running (active lock must not be reaped)", status)
	}
}

// ---------------------------------------------------------------------------
// Deleted webhook — job is silently completed
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Graceful shutdown
// ---------------------------------------------------------------------------

func TestWorker_gracefulShutdown_exitsOnCancel(t *testing.T) {
	database, svc := setup(t)
	w := newWorker(t, database, svc)

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(runDone)
	}()

	cancel()

	select {
	case <-runDone:
		// Run exited after ctx cancellation — correct.
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after context cancellation")
	}
}

func TestWorker_gracefulShutdown_waitUnblocksAfterRun(t *testing.T) {
	database, svc := setup(t)
	w := newWorker(t, database, svc)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	cancel()

	waitDone := make(chan struct{})
	go func() {
		w.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		// Wait returned after Run exited — correct.
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return within 2s after context cancellation")
	}
}

func TestWorker_gracefulShutdown_waitIsIdempotent(t *testing.T) {
	database, svc := setup(t)
	w := newWorker(t, database, svc)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	cancel()
	w.Wait() // first call

	// Second call must not block or panic (closed channel returns immediately).
	done := make(chan struct{})
	go func() {
		w.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Wait() call blocked")
	}
}

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
