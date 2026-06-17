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

// TestListBookings_adminSeesAllWithScope: an admin/owner requesting ?scope=all
// sees the whole workspace, with each booking labelled by its host's name.
func TestListBookings_adminSeesAllWithScope(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t) // setup user is the owner (admin)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	_, etID := seedEventTypeHTTP(t, h, key)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-other','` + etID + `','u2','2027-07-01T10:00:00Z','2027-07-01T10:30:00Z','confirmed')`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-mine','` + etID + `','` + ownerID + `','2027-07-01T12:00:00Z','2027-07-01T12:30:00Z','confirmed')`)

	req := authReq(http.MethodGet, "/v1/bookings?scope=all", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list bookings: %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			ID       string `json:"id"`
			HostName string `json:"host_name"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	host := map[string]string{}
	for _, it := range resp.Items {
		host[it.ID] = it.HostName
	}
	if _, ok := host["b-mine"]; !ok {
		t.Error("admin all-scope should include the owner's booking")
	}
	if _, ok := host["b-other"]; !ok {
		t.Error("admin all-scope should include another member's booking")
	}
	if host["b-other"] != "Two" {
		t.Errorf("b-other host_name = %q; want \"Two\"", host["b-other"])
	}
}

// TestListBookings_nonAdminScopeIgnored: ?scope=all from a non-admin is ignored —
// they still see only their own hosted bookings (no visibility escalation).
func TestListBookings_nonAdminScopeIgnored(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	const memberKey = "membertestkey"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(memberKey))
	_, etID := seedEventTypeHTTP(t, h, key)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-other','` + etID + `','u2','2027-07-01T10:00:00Z','2027-07-01T10:30:00Z','confirmed')`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b-mine','` + etID + `','` + ownerID + `','2027-07-01T12:00:00Z','2027-07-01T12:30:00Z','confirmed')`)

	req := authReq(http.MethodGet, "/v1/bookings?scope=all", "", memberKey)
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
	if !seen["b-other"] {
		t.Error("the member should see their own hosted booking")
	}
	if seen["b-mine"] {
		t.Error("scope=all must be ignored for non-admins — they must not see others' bookings")
	}
}
