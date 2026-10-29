package handler_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/zoom"
)

// Admins and the instance owner can list, view, edit and delete every event type on
// the instance (discussion #155); members keep seeing only what they own or host.
// Every request here goes through RequireAuth with an X-API-Key, so these tests are
// also the check that an admin's API key carries the admin's rights and a member's
// key does not: the role comes from the key's user row, not from the key.

type etAdminItem struct {
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Owned      bool   `json:"owned"`
	CanEdit    bool   `json:"can_edit"`
	OwnerID    string `json:"owner_id"`
	OwnerName  string `json:"owner_name"`
	OwnerEmail string `json:"owner_email"`
}

// adminWorkspace is a workspace with the instance owner (from /v1/setup), a plain
// admin, an event type owned by a member, and a second member who neither owns nor
// hosts it.
type adminWorkspace struct {
	h                          *handler.Handler
	db                         *sql.DB
	ownerKey, adminKey         string
	memberKey, otherKey        string
	slug, etID                 string
	adminID, memberID, otherID string
}

func setupAdminWorkspace(t *testing.T) adminWorkspace {
	t.Helper()
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	ws := adminWorkspace{
		h: h, db: database, ownerKey: ownerKey,
		adminKey: "admin-et-key", memberKey: "member-et-key", otherKey: "other-et-key",
		adminID: "u-admin", memberID: "u-member", otherID: "u-other",
	}
	for _, u := range []struct {
		id, email, name, key string
		admin                int
	}{
		{ws.adminID, "admin@example.com", "Ada Admin", ws.adminKey, 1},
		{ws.memberID, "member@example.com", "Mo Member", ws.memberKey, 0},
		{ws.otherID, "other@example.com", "Otto Other", ws.otherKey, 0},
	} {
		if _, err := database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES (?,?,?,'UTC',?)`, u.id, u.email, u.name, u.admin); err != nil {
			t.Fatalf("seed user %s: %v", u.id, err)
		}
		if _, err := database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES (?,?,'t',?,'2024-01-01')`, "k-"+u.id, u.id, sha256HexForTest(u.key)); err != nil {
			t.Fatalf("seed key %s: %v", u.id, err)
		}
	}
	ws.slug, ws.etID = seedEventTypeHTTP(t, h, ws.memberKey)
	return ws
}

func etCall(t *testing.T, h http.HandlerFunc, method, path, slug, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(method, path, body, key)
	if slug != "" {
		req.SetPathValue("slug", slug)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func listETs(t *testing.T, h *handler.Handler, key, query string) map[string]etAdminItem {
	t.Helper()
	rec := etCall(t, h.RequireAuth(h.ListEventTypes), http.MethodGet, "/v1/event-types"+query, "", "", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("list event types%s: %d — %s", query, rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []etAdminItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	out := map[string]etAdminItem{}
	for _, it := range resp.Items {
		out[it.Slug] = it
	}
	return out
}

func TestEventTypes_scopeAllListsEveryEventTypeForAdminsOnly(t *testing.T) {
	ws := setupAdminWorkspace(t)

	// An admin's own list stays their own: no foreign event types without asking.
	if _, ok := listETs(t, ws.h, ws.adminKey, "")[ws.slug]; ok {
		t.Error("admin default list includes a member's event type; want it only under ?scope=all")
	}

	for name, key := range map[string]string{"admin": ws.adminKey, "instance owner": ws.ownerKey} {
		it, ok := listETs(t, ws.h, key, "?scope=all")[ws.slug]
		if !ok {
			t.Fatalf("%s ?scope=all: member's event type missing", name)
		}
		if it.Owned || !it.CanEdit {
			t.Errorf("%s ?scope=all: owned=%v can_edit=%v; want owned=false can_edit=true", name, it.Owned, it.CanEdit)
		}
		if it.OwnerID != ws.memberID || it.OwnerName != "Mo Member" || it.OwnerEmail != "member@example.com" {
			t.Errorf("%s ?scope=all: owner = %q/%q/%q; want the member", name, it.OwnerID, it.OwnerName, it.OwnerEmail)
		}
	}

	// A member asking for scope=all is ignored, not refused, exactly like bookings.
	if _, ok := listETs(t, ws.h, ws.otherKey, "?scope=all")[ws.slug]; ok {
		t.Error("member ?scope=all sees another member's event type; scope=all must be admin-only")
	}
	// The owner's own view is unchanged.
	it, ok := listETs(t, ws.h, ws.memberKey, "")[ws.slug]
	if !ok || !it.Owned || !it.CanEdit || it.OwnerID != ws.memberID {
		t.Errorf("owner list: %+v (present=%v); want owned, editable, owner_id = self", it, ok)
	}
	if it.OwnerName != "" || it.OwnerEmail != "" {
		t.Errorf("owner list: owner_name/email = %q/%q; want empty for the owner's own event type", it.OwnerName, it.OwnerEmail)
	}
}

func TestEventTypes_adminManagesAnotherMembersEventType(t *testing.T) {
	ws := setupAdminWorkspace(t)
	h := ws.h
	path := "/v1/event-types/" + ws.slug

	rec := etCall(t, h.RequireAuth(h.GetEventType), http.MethodGet, path, ws.slug, "", ws.adminKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin get: %d — %s", rec.Code, rec.Body.String())
	}
	var got etAdminItem
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Owned || !got.CanEdit || got.OwnerID != ws.memberID || got.OwnerEmail == "" {
		t.Errorf("admin get: %+v; want owned=false can_edit=true and the member as owner", got)
	}

	rec = etCall(t, h.RequireAuth(h.PatchEventType), http.MethodPatch, path, ws.slug, `{"name":"Renamed by admin"}`, ws.adminKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin patch: %d — %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Name != "Renamed by admin" || got.Owned || !got.CanEdit || got.OwnerID != ws.memberID {
		t.Errorf("admin patch response: %+v; want the new name, owned=false, can_edit=true, owner = member", got)
	}
	var ownerID string
	if err := ws.db.QueryRow(`SELECT user_id FROM event_types WHERE id = ?`, ws.etID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerID != ws.memberID {
		t.Fatalf("event type owner after admin edit = %q; an edit must never move ownership", ownerID)
	}

	// The rest of the editor: hosts, questions, duplicate.
	if rec := putHosts(t, h, ws.slug, ws.adminKey, `{"hosts":[{"user_id":"`+ws.memberID+`","role":"rotation","priority":0},{"user_id":"`+ws.otherID+`","role":"rotation","priority":1}]}`); rec.Code != http.StatusOK {
		t.Fatalf("admin put hosts: %d — %s", rec.Code, rec.Body.String())
	}
	if hosts := getHosts(t, h, ws.slug, ws.adminKey); len(hosts.Items) != 2 {
		t.Errorf("admin get hosts: %d hosts; want 2", len(hosts.Items))
	}
	qPath := path + "/questions"
	rec = etCall(t, h.RequireAuth(h.CreateQuestion), http.MethodPost, qPath, ws.slug, `{"label":"Company","type":"text"}`, ws.adminKey)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create question: %d — %s", rec.Code, rec.Body.String())
	}
	var q struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &q)
	req := authReq(http.MethodPatch, qPath+"/"+q.ID, `{"label":"Company name"}`, ws.adminKey)
	req.SetPathValue("slug", ws.slug)
	req.SetPathValue("id", q.ID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.UpdateQuestion)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin update question: %d — %s", rec.Code, rec.Body.String())
	}
	rec = etCall(t, h.RequireAuth(h.ListQuestionsAdmin), http.MethodGet, qPath+"/admin", ws.slug, "", ws.adminKey)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Company name") {
		t.Fatalf("admin list questions: %d — %s", rec.Code, rec.Body.String())
	}

	rec = etCall(t, h.RequireAuth(h.DuplicateEventType), http.MethodPost, path+"/duplicate", ws.slug, "", ws.adminKey)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin duplicate: %d — %s", rec.Code, rec.Body.String())
	}
	var dup etAdminItem
	json.Unmarshal(rec.Body.Bytes(), &dup)
	if dup.OwnerID != ws.memberID || dup.Owned {
		t.Errorf("admin duplicate: owner_id=%q owned=%v; the copy must stay with the source's owner", dup.OwnerID, dup.Owned)
	}

	rec = etCall(t, h.RequireAuth(h.DeleteEventType), http.MethodDelete, "/v1/event-types/"+dup.Slug, dup.Slug, "", ws.adminKey)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: %d — %s", rec.Code, rec.Body.String())
	}
	var n int
	ws.db.QueryRow(`SELECT COUNT(*) FROM event_types WHERE slug = ?`, dup.Slug).Scan(&n)
	if n != 0 {
		t.Error("admin delete: event type still there")
	}
}

func TestEventTypes_memberCannotManageAnotherMembersEventType(t *testing.T) {
	ws := setupAdminWorkspace(t)
	h := ws.h
	path := "/v1/event-types/" + ws.slug

	for _, c := range []struct {
		name         string
		fn           http.HandlerFunc
		method, path string
		body         string
	}{
		{"get", h.GetEventType, http.MethodGet, path, ""},
		{"patch", h.PatchEventType, http.MethodPatch, path, `{"name":"Hijacked"}`},
		{"delete", h.DeleteEventType, http.MethodDelete, path, ""},
		{"get hosts", h.ListEventTypeHosts, http.MethodGet, path + "/hosts", ""},
		{"put hosts", h.SetEventTypeHosts, http.MethodPut, path + "/hosts", `{"hosts":[{"user_id":"u-other","role":"required","priority":0}]}`},
		{"list questions", h.ListQuestionsAdmin, http.MethodGet, path + "/questions/admin", ""},
		{"create question", h.CreateQuestion, http.MethodPost, path + "/questions", `{"label":"x","type":"text"}`},
		{"duplicate", h.DuplicateEventType, http.MethodPost, path + "/duplicate", ""},
		{"test email", h.SendTestEmail, http.MethodPost, path + "/test-email", `{"type":"confirmation"}`},
	} {
		if rec := etCall(t, h.RequireAuth(c.fn), c.method, c.path, ws.slug, c.body, ws.otherKey); rec.Code != http.StatusNotFound {
			t.Errorf("member %s on another member's event type: %d; want 404", c.name, rec.Code)
		}
	}
	var name, ownerID string
	if err := ws.db.QueryRow(`SELECT name, user_id FROM event_types WHERE id = ?`, ws.etID).Scan(&name, &ownerID); err != nil {
		t.Fatalf("event type gone after member requests: %v", err)
	}
	if name != "Test Meeting" || ownerID != ws.memberID {
		t.Errorf("event type changed by a member: name=%q owner=%q", name, ownerID)
	}
}

// An assigned host still sees the event type read-only: admin rights are not handed to
// hosts, and can_edit says so explicitly.
func TestEventTypes_hostStaysReadOnly(t *testing.T) {
	ws := setupAdminWorkspace(t)
	h := ws.h
	if _, err := ws.db.Exec(`INSERT INTO event_type_hosts (id,event_type_id,user_id,role,priority) VALUES ('eth-other',?,?,'rotation',1)`, ws.etID, ws.otherID); err != nil {
		t.Fatal(err)
	}
	it, ok := listETs(t, h, ws.otherKey, "")[ws.slug]
	if !ok || it.Owned || it.CanEdit {
		t.Errorf("host list: %+v (present=%v); want present, owned=false, can_edit=false", it, ok)
	}
	rec := etCall(t, h.RequireAuth(h.GetEventType), http.MethodGet, "/v1/event-types/"+ws.slug, ws.slug, "", ws.otherKey)
	var got etAdminItem
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.CanEdit || got.OwnerEmail != "member@example.com" {
		t.Errorf("host get: %d %+v; want 200, can_edit=false, the owner's email", rec.Code, got)
	}
	if rec := etCall(t, h.RequireAuth(h.PatchEventType), http.MethodPatch, "/v1/event-types/"+ws.slug, ws.slug, `{"name":"Hijacked"}`, ws.otherKey); rec.Code != http.StatusNotFound {
		t.Errorf("host patch: %d; want 404", rec.Code)
	}
}

// When an admin edits someone else's event type, per-owner checks still use the event
// type's OWNER. The location is the one such check in the edit path: a Zoom location
// with no manual link is valid only if the owner's Zoom account is connected, as when
// the owner edits it; the admin's own connection does not count.
func TestPatchEventType_adminEditValidatesLocationAgainstOwner(t *testing.T) {
	ws := setupAdminWorkspace(t)
	zc, err := zoom.New(ws.db, "client", "secret", "http://localhost/zoom/callback", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	ws.h.SetZoom(zc)
	patch := func() *httptest.ResponseRecorder {
		return etCall(t, ws.h.RequireAuth(ws.h.PatchEventType), http.MethodPatch, "/v1/event-types/"+ws.slug, ws.slug, `{"location_type":"zoom","location_value":""}`, ws.adminKey)
	}

	// Only the admin has Zoom: not good enough, the check is on the owner's account.
	if _, err := ws.db.Exec(`INSERT INTO zoom_connections (user_id, access_token_enc) VALUES (?, 'x')`, ws.adminID); err != nil {
		t.Fatal(err)
	}
	if rec := patch(); rec.Code != http.StatusBadRequest {
		t.Fatalf("zoom with only the admin connected: %d — %s; want 400, the location must be validated against the owner, not the admin", rec.Code, rec.Body.String())
	}

	// The owner connects Zoom: now the same admin edit is valid.
	if _, err := ws.db.Exec(`INSERT INTO zoom_connections (user_id, access_token_enc) VALUES (?, 'x')`, ws.memberID); err != nil {
		t.Fatal(err)
	}
	if rec := patch(); rec.Code != http.StatusOK {
		t.Fatalf("zoom with the owner connected: %d — %s; want 200", rec.Code, rec.Body.String())
	}
}
