package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

type etVisItem struct {
	Slug       string `json:"slug"`
	Owned      bool   `json:"owned"`
	OwnerName  string `json:"owner_name"`
	OwnerEmail string `json:"owner_email"`
}

// A member added as a host on someone else's event type can SEE it (read-only,
// owned=false) but cannot edit it; the owner sees it as owned=true.
func TestEventTypes_assignedHostSeesReadOnly(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	const memberKey = "hosttestkey"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(memberKey))
	slug, etID := seedEventTypeHTTP(t, h, key)
	// Assign u2 as a (non-owner) host on the owner's event type.
	if _, err := database.Exec(`INSERT INTO event_type_hosts (id,event_type_id,user_id,role,priority) VALUES ('eth2',?,'u2','rotation',1)`, etID); err != nil {
		t.Fatalf("seed host: %v", err)
	}

	// u2's list includes it, flagged owned=false.
	if got := listOwned(t, h, memberKey)[slug]; got == nil || *got != false {
		t.Fatalf("u2 list: event type owned flag = %v; want present and false", got)
	}
	// Owner's list shows it owned=true.
	if got := listOwned(t, h, key)[slug]; got == nil || *got != true {
		t.Fatalf("owner list: owned flag = %v; want present and true", got)
	}

	// u2 can GET it directly (read-only), owned=false.
	{
		req := authReq(http.MethodGet, "/v1/event-types/"+slug, "", memberKey)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.GetEventType)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("u2 get: %d — %s", rec.Code, rec.Body.String())
		}
		var et etVisItem
		json.Unmarshal(rec.Body.Bytes(), &et)
		if et.Owned {
			t.Error("u2 get: owned=true; want false (host, not owner)")
		}
		// The owner's identity is surfaced so the host knows who to contact.
		if et.OwnerEmail == "" {
			t.Error("u2 get: owner_email empty; want the owner's email for the read-only banner")
		}
	}

	// u2 cannot edit it — owner-only mutations stay closed (404).
	{
		req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"name":"Hijacked"}`, memberKey)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PatchEventType)(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("u2 patch: %d; want 404 (read-only host cannot edit)", rec.Code)
		}
	}
}

// listOwned returns slug → owned flag (pointer so "absent" is distinguishable).
func listOwned(t *testing.T, h *handler.Handler, key string) map[string]*bool {
	t.Helper()
	req := authReq(http.MethodGet, "/v1/event-types", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list event types: %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []etVisItem `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	out := make(map[string]*bool, len(resp.Items))
	for _, it := range resp.Items {
		owned := it.Owned
		out[it.Slug] = &owned
	}
	return out
}
