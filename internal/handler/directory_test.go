package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func patchMeHandle(t *testing.T, h patchMeHandler, apiKey, body string) (int, map[string]any) {
	t.Helper()
	rec := patchMe(t, h, body, apiKey)
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

type patchMeHandler interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	PatchMe(http.ResponseWriter, *http.Request)
}

func TestPatchMe_handleSetAndSlugified(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, resp := patchMeHandle(t, h, key, `{"handle":"  Wynne Pirini  "}`)
	if code != http.StatusOK {
		t.Fatalf("set handle: status = %d — %v", code, resp)
	}
	if resp["handle"] != "wynne-pirini" {
		t.Errorf("handle = %v; want wynne-pirini (slugified)", resp["handle"])
	}
}

func TestPatchMe_handleCollisionAndClear(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,handle) VALUES ('u2','m@example.com','Miri','UTC',0,'miri')`) //nolint:errcheck

	if code, _ := patchMeHandle(t, h, key, `{"handle":"miri"}`); code != http.StatusConflict {
		t.Errorf("collision: status = %d; want 409", code)
	}
	if code, _ := patchMeHandle(t, h, key, `{"handle":"!!!"}`); code != http.StatusBadRequest {
		t.Errorf("garbage: status = %d; want 400", code)
	}
	if code, resp := patchMeHandle(t, h, key, `{"handle":"mine"}`); code != http.StatusOK || resp["handle"] != "mine" {
		t.Fatalf("set: status = %d — %v", code, resp)
	}
	// Clearing removes the page.
	if code, resp := patchMeHandle(t, h, key, `{"handle":""}`); code != http.StatusOK || resp["handle"] != "" {
		t.Fatalf("clear: status = %d — %v", code, resp)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/u/mine", nil)
	req.SetPathValue("handle", "mine")
	h.PersonPage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("page after clear: status = %d; want 404", rec.Code)
	}
}

func personPage(t *testing.T, h interface {
	PersonPage(http.ResponseWriter, *http.Request)
}, handle string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/u/"+handle, nil)
	req.SetPathValue("handle", handle)
	h.PersonPage(rec, req)
	return rec.Code, rec.Body.String()
}

func TestPersonPage_listsPublicActiveEventTypes(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	if code, _ := patchMeHandle(t, h, key, `{"handle":"testhost"}`); code != http.StatusOK {
		t.Fatalf("set handle: %d", code)
	}
	slugVisible, _ := seedEventTypeHTTP(t, h, key)
	slugHidden, _ := seedEventTypeHTTP(t, h, key)
	// Hide one via is_public.
	req := authReq(http.MethodPatch, "/v1/event-types/"+slugHidden, `{"is_public":false}`, key)
	req.SetPathValue("slug", slugHidden)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hide event type: %d — %s", rec.Code, rec.Body.String())
	}

	code, body := personPage(t, h, "testhost")
	if code != http.StatusOK {
		t.Fatalf("person page: status = %d — %.200s", code, body)
	}
	if !strings.Contains(body, "/book/"+slugVisible) {
		t.Errorf("page missing visible event type link /book/%s", slugVisible)
	}
	if strings.Contains(body, "/book/"+slugHidden) {
		t.Errorf("page lists non-public event type /book/%s", slugHidden)
	}
}

func TestPersonPage_archivedAndUnknown404(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	if code, _ := patchMeHandle(t, h, key, `{"handle":"ghost"}`); code != http.StatusOK {
		t.Fatalf("set handle: %d", code)
	}
	database.Exec(`UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = ?`, userID) //nolint:errcheck

	if code, _ := personPage(t, h, "ghost"); code != http.StatusNotFound {
		t.Errorf("archived user page: status = %d; want 404", code)
	}
	if code, _ := personPage(t, h, "nobody-here"); code != http.StatusNotFound {
		t.Errorf("unknown handle page: status = %d; want 404", code)
	}
}

func TestTeamPage_listsMembersAndEventTypes(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,handle) VALUES ('u2','miri@example.com','Miri','UTC',0,'miri')`) //nolint:errcheck
	database.Exec(`INSERT INTO teams (id,name,slug) VALUES ('t1','Design','design')`)                                                       //nolint:errcheck
	database.Exec(`INSERT INTO team_members (id,team_id,user_id) VALUES ('tm1','t1','u2')`)                                                 //nolint:errcheck

	// Team-owned event type.
	slug, etID := seedEventTypeHTTP(t, h, key)
	database.Exec(`UPDATE event_types SET team_id = 't1' WHERE id = ?`, etID) //nolint:errcheck

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/team/design", nil)
	req.SetPathValue("slug", "design")
	h.TeamPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("team page: status = %d — %.200s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/book/"+slug) {
		t.Errorf("team page missing event type link /book/%s", slug)
	}
	if !strings.Contains(body, "/u/miri") {
		t.Errorf("team page missing member link /u/miri")
	}

	if code := func() int {
		r := httptest.NewRecorder()
		q := httptest.NewRequest(http.MethodGet, "/team/nope", nil)
		q.SetPathValue("slug", "nope")
		h.TeamPage(r, q)
		return r.Code
	}(); code != http.StatusNotFound {
		t.Errorf("unknown team page: status = %d; want 404", code)
	}
}

func TestPersonPage_includesHostedNotOwned(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,handle) VALUES ('u2','miri@example.com','Miri','UTC',0,'miri')`) //nolint:errcheck
	slug, etID := seedEventTypeHTTP(t, h, ownerKey)
	database.Exec(`INSERT INTO event_type_hosts (id,event_type_id,user_id,role) VALUES ('h1',?, 'u2','required')`, etID) //nolint:errcheck

	code, body := personPage(t, h, "miri")
	if code != http.StatusOK {
		t.Fatalf("person page: status = %d — %.200s", code, body)
	}
	if !strings.Contains(body, "/book/"+slug) {
		t.Errorf("hosted (not owned) event type missing from /u/miri")
	}
}
