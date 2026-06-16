package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListBookings_showsBookingsRoutedToOtherHosts covers the dashboard-visibility
// fix: a booking on an event type the viewer owns but hosted by ANOTHER member
// (e.g. a round-robin assignment) must still appear in the viewer's list.
func TestListBookings_showsBookingsRoutedToOtherHosts(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	slug, etID := seedEventTypeHTTP(t, h, key)
	_ = slug
	// A confirmed booking on this event type, hosted by u2 (not the owner/viewer).
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-other','`+etID+`','u2','2027-07-01T10:00:00Z','2027-07-01T10:30:00Z','confirmed')`)

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
	var found bool
	for _, it := range resp.Items {
		if it.ID == "b-other" {
			found = true
		}
	}
	if !found {
		t.Error("a booking hosted by another team member on the viewer's event type should appear in the dashboard")
	}
}
