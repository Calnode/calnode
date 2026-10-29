package handler

import (
	"log/slog"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

// Migration 00065 replaced the full UNIQUE(type, payload) index with a partial one over
// live jobs. SQLite only uses a partial index as an upsert target when the ON CONFLICT
// clause repeats its WHERE, so enqueueJob must keep matching it - and re-enqueueing a
// finished job must start a fresh run rather than conflict with history.
func TestEnqueueJob_upsertsLiveJobsAndRequeuesFinishedOnes(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	h := New(database, slog.Default())
	payload := map[string]string{"recording_id": "rec-1"}

	if err := h.enqueueJob(t.Context(), "notetaker.transcribe", payload); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := h.enqueueJob(t.Context(), "notetaker.transcribe", payload); err != nil {
		t.Fatalf("re-enqueue of a pending job: %v", err)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pending duplicate created %d jobs, want 1", n)
	}

	if _, err := database.Exec(`UPDATE jobs SET status = 'done'`); err != nil {
		t.Fatal(err)
	}
	if err := h.enqueueJob(t.Context(), "notetaker.transcribe", payload); err != nil {
		t.Fatalf("re-enqueue after completion: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = 'pending'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pending jobs after re-run = %d, want 1", n)
	}

	// A running job keeps running: resetting it to pending would run it twice.
	if _, err := database.Exec(`UPDATE jobs SET status = 'running', attempts = 1 WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if err := h.enqueueJob(t.Context(), "notetaker.transcribe", payload); err != nil {
		t.Fatalf("enqueue while running: %v", err)
	}
	var running, live int
	if err := database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = 'running' AND attempts = 1`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status IN ('pending', 'running')`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if running != 1 || live != 1 {
		t.Fatalf("running = %d, live = %d; want the running job untouched and no duplicate", running, live)
	}
}
