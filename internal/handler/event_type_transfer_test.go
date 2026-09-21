package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransferEventTypePreservesIdentityAndRequiresCurrentOwner(t *testing.T) {
	h, database, key, owner := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	_, err := database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('new-owner','new@example.com','New','Europe/Amsterdam',0)`)
	if err != nil {
		t.Fatal(err)
	}
	transfer := func() *httptest.ResponseRecorder {
		req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/transfer", fmt.Sprintf(`{"expected_owner_id":%q,"new_owner_id":"new-owner"}`, owner), key)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.TransferEventType)(rec, req)
		return rec
	}
	if rec := transfer(); rec.Code != http.StatusConflict {
		t.Fatalf("unassigned host transfer: %d %s", rec.Code, rec.Body)
	}
	if rec := putHosts(t, h, slug, key, `{"hosts":[{"user_id":"new-owner","role":"required","priority":0}]}`); rec.Code != http.StatusOK {
		t.Fatalf("assign: %d", rec.Code)
	}
	_, err = database.Exec(`INSERT INTO availability_rules (id,user_id,event_type_id,day_of_week,start_time,end_time) VALUES ('rule',?,?,1,'09:00','20:00')`, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('upcoming',?,?,'2099-01-01T09:00:00Z','2099-01-01T09:15:00Z','confirmed')`, id, owner)
	if err != nil {
		t.Fatal(err)
	}
	if rec := transfer(); rec.Code != http.StatusConflict {
		t.Fatalf("upcoming booking: %d", rec.Code)
	}
	if _, err = database.Exec(`UPDATE bookings SET status='cancelled' WHERE id='upcoming'`); err != nil {
		t.Fatal(err)
	}
	if rec := transfer(); rec.Code != http.StatusOK {
		t.Fatalf("transfer: %d %s", rec.Code, rec.Body)
	}
	var gotID, gotOwner, ruleOwner string
	if err := database.QueryRow(`SELECT id,user_id FROM event_types WHERE slug=?`, slug).Scan(&gotID, &gotOwner); err != nil {
		t.Fatal(err)
	}
	if gotID != id || gotOwner != "new-owner" {
		t.Fatalf("identity: %s %s", gotID, gotOwner)
	}
	if err := database.QueryRow(`SELECT user_id FROM availability_rules WHERE id='rule'`).Scan(&ruleOwner); err != nil {
		t.Fatal(err)
	}
	if ruleOwner != "new-owner" {
		t.Fatalf("rule owner: %s", ruleOwner)
	}
	if rec := transfer(); rec.Code != http.StatusConflict {
		t.Fatalf("stale transfer: %d", rec.Code)
	}
}
