package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

type hostsResp struct {
	Items []struct {
		UserID   string `json:"user_id"`
		Role     string `json:"role"`
		Priority int    `json:"priority"`
		Archived bool   `json:"archived"`
	} `json:"items"`
}

func getHosts(t *testing.T, h *handler.Handler, slug, key string) hostsResp {
	t.Helper()
	req := authReq(http.MethodGet, "/v1/event-types/"+slug+"/hosts", "", key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypeHosts)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get hosts: %d — %s", rec.Code, rec.Body.String())
	}
	var resp hostsResp
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp
}

func putHosts(t *testing.T, h *handler.Handler, slug, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPut, "/v1/event-types/"+slug+"/hosts", body, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SetEventTypeHosts)(rec, req)
	return rec
}

func TestEventTypeHosts_ownerSeededAsRequired(t *testing.T) {
	h, _, key, ownerID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	hosts := getHosts(t, h, slug, key)
	if len(hosts.Items) != 1 {
		t.Fatalf("got %d hosts; want 1 (owner seeded)", len(hosts.Items))
	}
	if hosts.Items[0].UserID != ownerID || hosts.Items[0].Role != "required" {
		t.Errorf("seeded host = %+v; want owner as required", hosts.Items[0])
	}
}

func TestEventTypeHosts_replaceWithRotation(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"rotation","priority":0},{"user_id":"u2","role":"rotation","priority":1}]}`, ownerID)
	if rec := putHosts(t, h, slug, key, body); rec.Code != http.StatusOK {
		t.Fatalf("put hosts: %d — %s", rec.Code, rec.Body.String())
	}
	hosts := getHosts(t, h, slug, key)
	if len(hosts.Items) != 2 {
		t.Fatalf("got %d hosts; want 2", len(hosts.Items))
	}
	for _, hh := range hosts.Items {
		if hh.Role != "rotation" {
			t.Errorf("host %s role = %s; want rotation", hh.UserID, hh.Role)
		}
	}
}

func TestEventTypeHosts_rejectsAllOptional(t *testing.T) {
	h, _, key, ownerID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	body := fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"optional","priority":0}]}`, ownerID)
	if rec := putHosts(t, h, slug, key, body); rec.Code != http.StatusBadRequest {
		t.Errorf("all-optional: got %d; want 400 — %s", rec.Code, rec.Body.String())
	}
}

func TestEventTypeHosts_rejectsEmpty(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	if rec := putHosts(t, h, slug, key, `{"hosts":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty: got %d; want 400", rec.Code)
	}
}

func TestEventTypeHosts_rejectsArchived(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,archived_at) VALUES ('u2','u2@example.com','Two','UTC',0,'2026-01-01T00:00:00Z')`)
	slug, _ := seedEventTypeHTTP(t, h, key)
	body := fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"required","priority":0},{"user_id":"u2","role":"rotation","priority":0}]}`, ownerID)
	if rec := putHosts(t, h, slug, key, body); rec.Code != http.StatusBadRequest {
		t.Errorf("archived host: got %d; want 400 — %s", rec.Code, rec.Body.String())
	}
}
