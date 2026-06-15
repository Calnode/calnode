package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/mailer"
)

// stubMailer is a non-Noop mailer used to simulate a configured SMTP server.
type stubMailer struct{ lastMsg mailer.Message }

func (m *stubMailer) Send(_ context.Context, msg mailer.Message) error {
	m.lastMsg = msg
	return nil
}

// ---------------------------------------------------------------------------
// POST /v1/event-types/{slug}/test-email
// ---------------------------------------------------------------------------

func TestSendTestEmail_success(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	stub := &stubMailer{}
	h.SetMailer(stub, "http://localhost") // enable email

	slug, _ := seedEventTypeHTTP(t, h, key)

	for _, typ := range []string{"confirmation", "cancellation", "reschedule", "reminder"} {
		stub.lastMsg = mailer.Message{}
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
		// Verify the mailer received a message with [TEST] prefix.
		if len(stub.lastMsg.To) == 0 {
			t.Errorf("type=%s: mailer.Send not called", typ)
		} else if stub.lastMsg.Subject[:7] != "[TEST] " {
			t.Errorf("type=%s: subject %q; want [TEST] prefix", typ, stub.lastMsg.Subject)
		}
	}
}

func TestSendTestEmail_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	h.SetMailer(&stubMailer{}, "http://localhost")

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
	h.SetMailer(&stubMailer{}, "http://localhost")
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
	h.SetMailer(&stubMailer{}, "http://localhost")
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := httptest.NewRequest(http.MethodPost, "/v1/event-types/"+slug+"/test-email", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

func TestSendTestEmail_emailNotConfigured(t *testing.T) {
	// setupWorkspaceWithDB uses handler.New which leaves mailer as Noop → 503.
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/test-email",
		`{"type":"confirmation"}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d; want 503 (noop mailer)", rec.Code)
	}
}

func TestSendTestEmail_wrongSlug(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	h.SetMailer(&stubMailer{}, "http://localhost")
	_, _ = seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPost, "/v1/event-types/other-slug/test-email",
		`{"type":"confirmation"}`, key)
	req.SetPathValue("slug", "other-slug")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong slug: got %d; want 404", rec.Code)
	}
}
