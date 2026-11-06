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

func patchVisibility(t *testing.T, h *handler.Handler, key, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, body, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	return rec
}

func assertVisibility(t *testing.T, rec *httptest.ResponseRecorder, status int, visibility string) {
	t.Helper()
	got := mustJSON(t, rec, status, "event type visibility")
	if got["visibility"] != visibility || got["is_public"] != (visibility != "private") {
		t.Fatalf("visibility = %v, is_public = %v; want %s, %t", got["visibility"], got["is_public"], visibility, visibility != "private")
	}
}

func TestEventTypeVisibility_createViewUpdateAndDuplicate(t *testing.T) {
	for _, visibility := range []string{"public", "unlisted", "private", "default"} {
		t.Run(visibility, func(t *testing.T) {
			h, key, _ := setupWorkspace(t)
			body := `{"slug":"visibility-test","name":"Visibility test","duration_minutes":30}`
			want := visibility
			if visibility == "default" {
				want = "public"
			} else {
				body = strings.TrimSuffix(body, "}") + fmt.Sprintf(`,"visibility":%q}`, visibility)
			}
			rec := httptest.NewRecorder()
			h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, key))
			assertVisibility(t, rec, http.StatusCreated, want)

			// Exercise every transition, including toggling Active without losing visibility.
			for _, state := range []string{want, "private", "unlisted", "public", "unlisted"} {
				assertVisibility(t, patchVisibility(t, h, key, "visibility-test", fmt.Sprintf(`{"visibility":%q}`, state)), http.StatusOK, state)
				for _, active := range []bool{false, true} {
					rec = patchVisibility(t, h, key, "visibility-test", fmt.Sprintf(`{"is_active":%t}`, active))
					assertVisibility(t, rec, http.StatusOK, state)
					if got := mustJSON(t, rec, http.StatusOK, "active state")["is_active"]; got != active {
						t.Fatalf("is_active = %v; want %t", got, active)
					}
				}
				req := authReq(http.MethodGet, "/v1/event-types/visibility-test", "", key)
				req.SetPathValue("slug", "visibility-test")
				rec = httptest.NewRecorder()
				h.RequireAuth(h.GetEventType)(rec, req)
				assertVisibility(t, rec, http.StatusOK, state)
			}

			rec = httptest.NewRecorder()
			h.RequireAuth(h.ListEventTypes)(rec, authReq(http.MethodGet, "/v1/event-types", "", key))
			mustStatus(t, rec, http.StatusOK, "admin list")
			var list struct {
				Items []struct{ Slug, Visibility string }
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Items) != 1 || list.Items[0].Slug != "visibility-test" || list.Items[0].Visibility != "unlisted" {
				t.Fatalf("admin list lost unlisted event type: %s", rec.Body.String())
			}
			rec = duplicate(t, h, key, "visibility-test")
			assertVisibility(t, rec, http.StatusCreated, "unlisted")
			if mustJSON(t, rec, http.StatusCreated, "duplicate")["is_active"] != false {
				t.Fatal("duplicate must remain inactive")
			}
		})
	}
}

func TestEventTypeVisibility_rejectInvalidAndSupportLegacyPublic(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	for _, value := range []string{`"secret"`, `""`, `"PUBLIC"`, `false`, `1`} {
		body := fmt.Sprintf(`{"slug":"bad-visibility","name":"Bad","duration_minutes":30,"visibility":%s}`, value)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, key))
		mustStatus(t, rec, http.StatusBadRequest, "invalid create")
		rec = patchVisibility(t, h, key, slug, fmt.Sprintf(`{"visibility":%s}`, value))
		mustStatus(t, rec, http.StatusBadRequest, "invalid patch")
	}
	for _, body := range []string{`{"visibility":"private","is_public":true}`, `{"visibility":"unlisted","is_public":false}`} {
		mustStatus(t, patchVisibility(t, h, key, slug, body), http.StatusBadRequest, "conflicting flags")
	}
	assertVisibility(t, patchVisibility(t, h, key, slug, `{"visibility":"unlisted","is_public":true}`), http.StatusOK, "unlisted")
	assertVisibility(t, patchVisibility(t, h, key, slug, `{"is_public":false}`), http.StatusOK, "private")
	assertVisibility(t, patchVisibility(t, h, key, slug, `{"is_public":true}`), http.StatusOK, "public")
}

func TestEventTypeVisibility_directBookingAndDirectories(t *testing.T) {
	for _, visibility := range []string{"public", "unlisted", "private"} {
		for _, active := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/active=%t", visibility, active), func(t *testing.T) {
				h, database, key, ownerID := setupWorkspaceWithDB(t)
				slug, etID := seedEventTypeHTTP(t, h, key)
				mustExec(t, database, `UPDATE users SET handle = 'visibility-host' WHERE id = ?`, ownerID)
				mustExec(t, database, `INSERT INTO teams (id,name,slug) VALUES ('vis-team','Visibility team','vis-team')`)
				mustExec(t, database, `UPDATE event_types SET team_id = 'vis-team' WHERE id = ?`, etID)
				mustExec(t, database, `INSERT INTO event_type_questions (id,event_type_id,label,type,required,position) VALUES ('vis-q',?,'Agenda','text',1,0)`, etID)
				rec := patchVisibility(t, h, key, slug, fmt.Sprintf(`{"visibility":%q,"is_active":%t}`, visibility, active))
				assertVisibility(t, rec, http.StatusOK, visibility)

				for _, directory := range []struct {
					path, param, value string
					handler            http.HandlerFunc
				}{
					{"/u/visibility-host", "handle", "visibility-host", h.PersonPage},
					{"/team/vis-team", "slug", "vis-team", h.TeamPage},
				} {
					req := httptest.NewRequest(http.MethodGet, directory.path, nil)
					req.SetPathValue(directory.param, directory.value)
					rec = httptest.NewRecorder()
					directory.handler(rec, req)
					mustStatus(t, rec, http.StatusOK, directory.path)
					listed := strings.Contains(rec.Body.String(), "/book/"+slug)
					if listed != (active && visibility == "public") {
						t.Errorf("%s listed = %t", directory.path, listed)
					}
				}

				bookable := active && visibility != "private"
				want := http.StatusNotFound
				if bookable {
					want = http.StatusOK
				}
				for _, endpoint := range []struct {
					path    string
					handler http.HandlerFunc
				}{
					{"/book/" + slug, h.BookPage},
					{"/v1/event-types/" + slug + "/public", h.PublicEventType},
					{"/v1/event-types/" + slug + "/questions", h.ListQuestions},
					{fmt.Sprintf("/v1/event-types/%s/slots?from=%s&to=%s&tz=UTC", slug, nextMonday(), nextMonday()), h.GetSlots},
				} {
					req := httptest.NewRequest(http.MethodGet, endpoint.path, nil)
					req.SetPathValue("slug", slug)
					rec = httptest.NewRecorder()
					endpoint.handler(rec, req)
					mustStatus(t, rec, want, endpoint.path)
					if bookable && strings.Contains(endpoint.path, "/slots?") {
						var slots struct{ Slots []slotResp }
						if err := json.Unmarshal(rec.Body.Bytes(), &slots); err != nil || len(slots.Slots) == 0 {
							t.Fatalf("bookable event has no slots: %v — %s", err, rec.Body.String())
						}
					}
				}

				// The direct link uses the normal intake validation and booking engine.
				book := func(answers string) *httptest.ResponseRecorder {
					body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Guest","email":"guest@example.com","answers":%s}`, slug, answers)
					req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
					rec := httptest.NewRecorder()
					h.CreateBooking(rec, req)
					return rec
				}
				if bookable {
					mustStatus(t, book(`[]`), http.StatusBadRequest, "required question")
					got := mustJSON(t, book(`[{"question_id":"vis-q","value":"Discuss"}]`), http.StatusCreated, "direct booking")
					if got["status"] != "confirmed" {
						t.Fatalf("booking status = %v; want confirmed", got["status"])
					}
					mustStatus(t, book(`[{"question_id":"vis-q","value":"Discuss"}]`), http.StatusConflict, "double booking")
				} else {
					mustStatus(t, book(`[{"question_id":"vis-q","value":"Discuss"}]`), http.StatusNotFound, "booking blocked")
				}
			})
		}
	}
}
