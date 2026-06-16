package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListBookings_hostOnly locks in strict host-only visibility: a member sees
// only the bookings they are the assigned host of. Even the owner of the event
// type does NOT see a booking routed to another team member (round-robin), and
// does see one routed to themselves.
func TestListBookings_hostOnly(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	_, etID := seedEventTypeHTTP(t, h, key) // owned by the setup user (the viewer)

	// One booking hosted by another member, one hosted by the viewer.
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-other','` + etID + `','u2','2027-07-01T10:00:00Z','2027-07-01T10:30:00Z','confirmed')`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-mine','` + etID + `','` + ownerID + `','2027-07-01T12:00:00Z','2027-07-01T12:30:00Z','confirmed')`)

	req := authReq(http.MethodGet, "/v1/bookings", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list bookings: %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	seen := map[string]bool{}
	for _, it := range resp.Items {
		seen[it.ID] = true
	}
	if !seen["b-mine"] {
		t.Error("the viewer should see the booking they host")
	}
	if seen["b-other"] {
		t.Error("the viewer should NOT see a booking hosted by another member, even on their own event type")
	}
}
