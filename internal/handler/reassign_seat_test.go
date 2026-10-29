package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The primary seat (booking_hosts) must move with bookings.host_id. Availability reads
// booking_hosts, so a seat left on the old host made the new host bookable twice.
func TestReassignBooking_movesPrimarySeat(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','h2@example.com','Host2','UTC',0)`)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','h3@example.com','Host3','UTC',0)`)
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','Intro',30)`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u2','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed')`)
	database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary,external_event_id,external_calendar_id,external_provider)
		VALUES ('bh1','b1','u2',1,'evt-old','primary','google')`)

	req := authReq(http.MethodPost, "/v1/bookings/b1/reassign", `{"host_id":"u3"}`, ownerKey)
	req.SetPathValue("id", "b1")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ReassignBooking)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign: got %d — %s", rec.Code, rec.Body.String())
	}
	var seat, eventID string
	if err := database.QueryRow(`SELECT user_id, COALESCE(external_event_id,'') FROM booking_hosts WHERE booking_id='b1' AND is_primary=1`).Scan(&seat, &eventID); err != nil {
		t.Fatal(err)
	}
	if seat != "u3" || eventID != "" {
		t.Errorf("primary seat = %q (event %q); want u3 with the old host's event id cleared", seat, eventID)
	}
}

// Reassigning onto a co-host would collapse two seats into one: refuse it cleanly instead
// of a 500 from UNIQUE(booking_id, user_id), leaving the booking as it was.
func TestReassignBooking_rejectsExistingCoHost(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','h2@example.com','Host2','UTC',0)`)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','h3@example.com','Host3','UTC',0)`)
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','Intro',30)`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u2','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed')`)
	database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary) VALUES ('bh1','b1','u2',1)`)
	database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary) VALUES ('bh2','b1','u3',0)`)

	req := authReq(http.MethodPost, "/v1/bookings/b1/reassign", `{"host_id":"u3"}`, ownerKey)
	req.SetPathValue("id", "b1")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ReassignBooking)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d; want 409 — %s", rec.Code, rec.Body.String())
	}
	var hostID string
	database.QueryRow(`SELECT host_id FROM bookings WHERE id='b1'`).Scan(&hostID)
	if hostID != "u2" {
		t.Errorf("host_id = %q; want u2 (unchanged)", hostID)
	}
}
