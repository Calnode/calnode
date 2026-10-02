package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

func patchDirectoryOrder(t *testing.T, h *handler.Handler, key, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, body, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	return rec
}

func assertDisplayOrder(t *testing.T, rec *httptest.ResponseRecorder, status, want int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d; want %d: %s", rec.Code, status, rec.Body.String())
	}
	var resp struct {
		DisplayOrder *int `json:"display_order"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.DisplayOrder == nil || *resp.DisplayOrder != want {
		t.Fatalf("display_order missing or wrong; want %d: %s", want, rec.Body.String())
	}
}

func TestEventType_displayOrder(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	for i, value := range []string{"", `,"display_order":-10`, `,"display_order":20`} {
		slug := fmt.Sprintf("ordered-%d", i)
		req := authReq(http.MethodPost, "/v1/event-types", fmt.Sprintf(`{"slug":%q,"name":"Meeting","duration_minutes":30%s}`, slug, value), key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, req)
		assertDisplayOrder(t, rec, http.StatusCreated, []int{0, -10, 20}[i])
	}

	const slug = "ordered-1"
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"name":"Renamed"}`, -10}, // omitted fields survive partial updates
		{`{"display_order":null}`, -10},
		{`{"display_order":7}`, 7},
		{`{"display_order":0}`, 0}, // explicit zero resets the order
		{`{"display_order":-5}`, -5},
	} {
		assertDisplayOrder(t, patchDirectoryOrder(t, h, key, slug, tc.body), http.StatusOK, tc.want)
	}
	for _, body := range []string{`{"display_order":1.5}`, `{"display_order":"1"}`, `{"display_order":9223372036854775808}`} {
		t.Run(body, func(t *testing.T) {
			if rec := patchDirectoryOrder(t, h, key, slug, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid order: status = %d: %s", rec.Code, rec.Body.String())
			}
			req := authReq(http.MethodPost, "/v1/event-types", strings.Replace(body, "{", `{"slug":"invalid-order","name":"Meeting","duration_minutes":30,`, 1), key)
			rec := httptest.NewRecorder()
			h.RequireAuth(h.CreateEventType)(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid create order: status = %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
	seedMember(t, database, "order-member", "order-member@example.com")
	const memberKey = "directory-order-member-key"
	mustExec(t, database, `INSERT INTO api_keys (id,user_id,name,key_hash) VALUES ('order-key','order-member','test',?)`, sha256HexForTest(memberKey))
	mustExec(t, database, `INSERT INTO event_type_hosts (id,event_type_id,user_id,role) SELECT 'order-host',id,'order-member','required' FROM event_types WHERE slug = ?`, slug)
	if rec := patchDirectoryOrder(t, h, memberKey, slug, `{"display_order":100}`); rec.Code != http.StatusNotFound {
		t.Fatalf("host can change order: status = %d", rec.Code)
	}
	// GET/list use the same scan contract as create/PATCH; check both explicitly.
	req := authReq(http.MethodGet, "/v1/event-types/"+slug, "", key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetEventType)(rec, req)
	assertDisplayOrder(t, rec, http.StatusOK, -5)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, authReq(http.MethodGet, "/v1/event-types", "", key))
	var list struct {
		Items []struct {
			Slug         string `json:"slug"`
			DisplayOrder *int   `json:"display_order"`
		} `json:"items"`
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list.Items {
		if item.Slug == slug {
			found = true
			if item.DisplayOrder == nil || *item.DisplayOrder != -5 {
				t.Fatalf("list lost display_order: %s", rec.Body.String())
			}
		}
	}
	if !found {
		t.Fatal("list missing event")
	}
	assertDisplayOrder(t, duplicate(t, h, key, slug), http.StatusCreated, -5)
}

func TestDirectories_displayOrder(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	mustExec(t, database, `UPDATE users SET handle = 'ordered-host' WHERE id = ?`, ownerID)
	seedMember(t, database, "other-owner", "other-owner@example.com")
	mustExec(t, database, `INSERT INTO teams (id,name,slug) VALUES ('order-team','Order team','order-team')`)
	// Insert in a different order from the desired output. Slugs deliberately
	// disagree with names, so sorting solely by slug cannot pass this test.
	for _, item := range []struct {
		slug, name string
		order      int
	}{
		{"a-last", "Alpha", 10},
		{"z-first", "Zulu", -10},
		{"b-beta", "Beta", 0},
		{"d-alpha", "Alpha", 0},
		{"c-alpha", "Alpha", 0},
	} {
		mustExec(t, database, `INSERT INTO event_types (id,user_id,team_id,slug,name,duration_minutes,display_order) VALUES (?,?,'order-team',?,?,30,?)`, item.slug, ownerID, item.slug, item.name, item.order)
	}
	// A hosted event participates in the same ordering as owned events.
	mustExec(t, database, `UPDATE event_types SET user_id = 'other-owner' WHERE slug = 'b-beta'`)
	mustExec(t, database, `INSERT INTO event_type_hosts (id,event_type_id,user_id,role) VALUES ('order-host','b-beta',?,'required')`, ownerID)
	for _, slug := range []string{"hidden", "inactive", "unrelated"} {
		mustExec(t, database, `INSERT INTO event_types (id,user_id,team_id,slug,name,duration_minutes,display_order) VALUES (?,?,'order-team',?, ?,30,-100)`, slug, ownerID, slug, slug)
	}
	mustExec(t, database, `UPDATE event_types SET is_public = 0 WHERE slug = 'hidden'`)
	mustExec(t, database, `UPDATE event_types SET is_active = 0 WHERE slug = 'inactive'`)
	mustExec(t, database, `UPDATE event_types SET user_id = 'other-owner', team_id = NULL WHERE slug = 'unrelated'`)
	checkPages := func(want []string) {
		t.Helper()
		for _, page := range []string{"person", "team"} {
			t.Run(page+strings.Join(want, ","), func(t *testing.T) {
				var code int
				var body string
				if page == "person" {
					code, body = personPage(t, h, "ordered-host")
				} else {
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodGet, "/team/order-team", nil)
					req.SetPathValue("slug", "order-team")
					h.TeamPage(rec, req)
					code, body = rec.Code, rec.Body.String()
				}
				if code != http.StatusOK {
					t.Fatalf("page: %d: %s", code, body)
				}
				previous := -1
				for _, slug := range want {
					index := strings.Index(body, `href="/book/`+slug+`"`)
					if index <= previous {
						t.Fatalf("missing or out-of-order %s; want %v", slug, want)
					}
					previous = index
				}
				for _, slug := range []string{"hidden", "inactive", "unrelated"} {
					if strings.Contains(body, `href="/book/`+slug+`"`) {
						t.Errorf("page contains %s", slug)
					}
				}
			})
		}
	}
	checkPages([]string{"z-first", "c-alpha", "d-alpha", "b-beta", "a-last"})
	assertDisplayOrder(t, patchDirectoryOrder(t, h, key, "a-last", `{"display_order":-20}`), http.StatusOK, -20)
	checkPages([]string{"a-last", "z-first", "c-alpha", "d-alpha", "b-beta"})
	// All-zero defaults retain alphabetical ordering with deterministic slug ties.
	mustExec(t, database, `UPDATE event_types SET display_order = 0`)
	checkPages([]string{"a-last", "c-alpha", "d-alpha", "b-beta", "z-first"})
}
