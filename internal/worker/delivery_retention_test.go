package worker_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// seedDelivery inserts one webhook_deliveries row with an explicit status and
// last_attempted_at. A nil attemptedAt leaves the column NULL, which is what a
// delivery that has never been tried looks like.
func seedDelivery(t *testing.T, database *sql.DB, id, webhookID, status string, attemptedAt *time.Time) {
	t.Helper()
	var at any
	if attemptedAt != nil {
		at = attemptedAt.UTC().Format(time.RFC3339)
	}
	if _, err := database.Exec(`
		INSERT INTO webhook_deliveries (id, webhook_id, event, payload, status, last_attempted_at)
		VALUES (?, ?, 'booking.created', '{}', ?, ?)`,
		id, webhookID, status, at); err != nil {
		t.Fatalf("seed delivery %s: %v", id, err)
	}
}

func deliveryExists(t *testing.T, database *sql.DB, id string) bool {
	t.Helper()
	var n int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM webhook_deliveries WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// TestPoll_purgesOldFinishedDeliveries covers a table that nothing ever swept.
//
// webhook_deliveries is a log - the UI shows the 50 most recent and no code reads it
// back for state - but rows were kept for the entire life of the instance, inside the
// SQLite file Litestream replicates offsite. Five other tables are already purged by
// this same job; this one was simply missed.
//
// The retention rule is deliberately narrow, and the pending case below is why: a
// pending delivery still has a jobs row referring to it by id, so deleting one would
// convert a deliverable webhook into a permanent failure. Only rows that reached a
// terminal status, and therefore have already been attempted, may be swept.
func TestPoll_purgesOldFinishedDeliveries(t *testing.T) {
	database, svc := setup(t)
	wh, _, err := svc.Create(context.Background(), "host-01", "https://example.test/hook", nil)
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	old := time.Now().UTC().Add(-40 * 24 * time.Hour)   // past the 30-day window
	recent := time.Now().UTC().Add(-2 * 24 * time.Hour) // inside it

	seedDelivery(t, database, "d-old-success", wh.ID, "success", &old)
	seedDelivery(t, database, "d-old-failed", wh.ID, "failed", &old)
	seedDelivery(t, database, "d-recent-success", wh.ID, "success", &recent)
	seedDelivery(t, database, "d-old-pending", wh.ID, "pending", &old)
	seedDelivery(t, database, "d-never-attempted", wh.ID, "pending", nil)

	newWorker(t, database, svc).Poll(context.Background())

	for _, c := range []struct {
		id     string
		want   bool
		reason string
	}{
		{"d-old-success", false, "a delivered webhook past the retention window should be swept"},
		{"d-old-failed", false, "a permanently failed webhook past the window should be swept"},
		{"d-recent-success", true, "recent deliveries stay: they are what someone reads when investigating"},
		{"d-old-pending", true, "a pending delivery still has a jobs row pointing at it; " +
			"deleting it would turn a deliverable webhook into a permanent failure"},
		{"d-never-attempted", true, "never attempted, so nothing proves it is finished"},
	} {
		if got := deliveryExists(t, database, c.id); got != c.want {
			t.Errorf("%s: exists = %v, want %v - %s", c.id, got, c.want, c.reason)
		}
	}
}
