package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// makeRoundRobin switches an event type to round-robin with the given rotation
// host ids (in priority order).
func makeRoundRobin(t *testing.T, h *handler.Handler, slug, key string, hostIDs ...string) {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"routing_mode":"round_robin"}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set round_robin: %d — %s", rec.Code, rec.Body.String())
	}
	hosts := ""
	for i, id := range hostIDs {
		if i > 0 {
			hosts += ","
		}
		hosts += fmt.Sprintf(`{"user_id":%q,"role":"rotation","priority":%d}`, id, i)
	}
	if rec := putHosts(t, h, slug, key, `{"hosts":[`+hosts+`]}`); rec.Code != http.StatusOK {
		t.Fatalf("set rotation hosts: %d — %s", rec.Code, rec.Body.String())
	}
}

func TestRoundRobin_evenDistribution(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	slug, etID := seedEventTypeHTTP(t, h, key)
	makeRoundRobin(t, h, slug, key, ownerID, "u2")

	// Two non-overlapping future slots → least-loaded each time, so they should
	// land on different hosts (booking 1 → owner on the tie, booking 2 → u2).
	if rec := postBooking(t, h, slug, "2027-05-01T10:00:00Z", "Alice", "alice@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("booking 1: %d — %s", rec.Code, rec.Body.String())
	}
	if rec := postBooking(t, h, slug, "2027-05-01T12:00:00Z", "Bob", "bob@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("booking 2: %d — %s", rec.Code, rec.Body.String())
	}

	rows, _ := database.Query(`SELECT host_id FROM bookings WHERE event_type_id=? ORDER BY start_at`, etID)
	defer rows.Close()
	var hostIDs []string
	for rows.Next() {
		var hid string
		rows.Scan(&hid)
		hostIDs = append(hostIDs, hid)
	}
	if len(hostIDs) != 2 {
		t.Fatalf("got %d bookings; want 2", len(hostIDs))
	}
	if hostIDs[0] == hostIDs[1] {
		t.Errorf("both bookings went to %s; round-robin should distribute across the two hosts", hostIDs[0])
	}
}

func TestRoundRobin_skipsBusyHost(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	slug, _ := seedEventTypeHTTP(t, h, key)
	makeRoundRobin(t, h, slug, key, ownerID, "u2")

	const slot = "2027-06-01T10:00:00Z"
	// First booking at the slot → owner (tie, priority order).
	if rec := postBooking(t, h, slug, slot, "Alice", "alice@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("booking 1: %d — %s", rec.Code, rec.Body.String())
	}
	// Second booking at the SAME slot → owner is now busy, so it must go to u2.
	if rec := postBooking(t, h, slug, slot, "Bob", "bob@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("booking 2 (same slot): %d — %s", rec.Code, rec.Body.String())
	}

	var u2count int
	database.QueryRow(`SELECT COUNT(*) FROM bookings WHERE host_id='u2'`).Scan(&u2count)
	if u2count != 1 {
		t.Errorf("expected the second booking to be assigned to the free host u2; u2 has %d bookings", u2count)
	}
}
