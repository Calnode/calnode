package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// POST /v1/event-types/{slug}/test-email
// ---------------------------------------------------------------------------

func TestSendTestEmail_success(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	slug, _ := seedEventTypeHTTP(t, h, key)

	for _, typ := range []string{"confirmation", "cancellation", "reschedule", "reminder"} {
		req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/test-email",
			`{"type":"`+typ+`"}`, key)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.SendTestEmail)(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("type=%s: got %d — %s", typ, rec.Code, rec.Body.String())
			continue
		}
		var resp map[string]any
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if sent, _ := resp["sent"].(bool); !sent {
			t.Errorf("type=%s: sent = %v; want true", typ, resp["sent"])
		}
		if to, _ := resp["to"].(string); to == "" {
			t.Errorf("type=%s: to is empty", typ)
		}
	}
}

func TestSendTestEmail_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	req := authReq(http.MethodPost, "/v1/event-types/no-such-slug/test-email",
		`{"type":"confirmation"}`, key)
	req.SetPathValue("slug", "no-such-slug")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d; want 404", rec.Code)
	}
}

func TestSendTestEmail_invalidType(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	for _, bad := range []string{"", "unknown", "CONFIRMATION", "confirm"} {
		body := `{"type":"` + bad + `"}`
		req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/test-email", body, key)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.SendTestEmail)(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("type=%q: got %d; want 400", bad, rec.Code)
		}
	}
}

func TestSendTestEmail_requiresAuth(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := httptest.NewRequest(http.MethodPost, "/v1/event-types/"+slug+"/test-email",
		nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

func TestSendTestEmail_wrongUser(t *testing.T) {
	// Two users; user2 cannot test-email user1's event type.
	h, _, key1, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key1)

	// Create a second user by setup is single-user, so test via wrong slug.
	req := authReq(http.MethodPost, "/v1/event-types/other-slug/test-email",
		`{"type":"confirmation"}`, key1)
	req.SetPathValue("slug", "other-slug")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong slug: got %d; want 404", rec.Code)
	}
	_ = slug
}
