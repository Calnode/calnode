package handler_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// seedCheckboxQuestion adds a checkbox question to slug and returns its id.
func seedCheckboxQuestion(t *testing.T, h *handler.Handler, slug, apiKey, label string, required bool) string {
	t.Helper()
	body := fmt.Sprintf(`{"label":%q,"type":"checkbox","required":%t}`, label, required)
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", body, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create question: %d — %s", rec.Code, rec.Body.String())
	}
	var q struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &q) //nolint:errcheck
	return q.ID
}

// bookWithAnswer posts a booking carrying answersJSON (pass "" for none).
func bookWithAnswer(t *testing.T, h *handler.Handler, slug, startAt, email, answersJSON string) *httptest.ResponseRecorder {
	t.Helper()
	answers := ""
	if answersJSON != "" {
		answers = `,"answers":` + answersJSON
	}
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Ana","email":%q,"timezone":"UTC"%s}`,
		slug, startAt, email, answers)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	return rec
}

// TestRequiredCheckbox_isEnforced covers the bug where a required checkbox — the natural
// way to build a consent gate ("I agree to the terms") — was not enforced at all on the
// booking page. book.html sends "no" when unticked, which is a NON-EMPTY answer, so the
// generic required rule treated it as satisfied and the booking was created. The embed
// widget only blocked it by accident, because it omitted the answer entirely and tripped
// the "missing" path instead.
func TestRequiredCheckbox_isEnforced(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	seedFullAvailabilityDB(t, database, ownerIDOf(t, database))
	qid := seedCheckboxQuestion(t, h, slug, apiKey, "I agree to the terms", true)

	// Unticked, sent as "no" — what book.html submits. Previously created the booking.
	rec := bookWithAnswer(t, h, slug, "2027-05-03T10:00:00Z", "a@example.com",
		fmt.Sprintf(`[{"question_id":%q,"value":"no"}]`, qid))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unticked required checkbox (\"no\") = %d; want 400 — %s", rec.Code, rec.Body.String())
	}

	// Omitted entirely — what the embed widget used to submit.
	rec = bookWithAnswer(t, h, slug, "2027-05-03T11:00:00Z", "b@example.com", `[]`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("omitted required checkbox = %d; want 400 — %s", rec.Code, rec.Body.String())
	}

	// Ticked — books.
	rec = bookWithAnswer(t, h, slug, "2027-05-03T12:00:00Z", "c@example.com",
		fmt.Sprintf(`[{"question_id":%q,"value":"yes"}]`, qid))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ticked required checkbox = %d; want 201 — %s", rec.Code, rec.Body.String())
	}
}

// TestCheckboxAnswer_normalisedToYesNo pins that the stored value is canonical regardless
// of which surface (or API/MCP caller) produced it — the booking page sent "yes"/"no", the
// widget sent "Yes", and a model might send "true". All three used to land in the DB
// verbatim, so the same question read three different ways in exports and webhooks.
func TestCheckboxAnswer_normalisedToYesNo(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	seedFullAvailabilityDB(t, database, ownerIDOf(t, database))
	qid := seedCheckboxQuestion(t, h, slug, apiKey, "Subscribe", false) // optional
	cases := []struct{ sent, want string }{
		{"yes", "yes"}, {"Yes", "yes"}, {"true", "yes"}, {"on", "yes"}, {"1", "yes"},
		{"no", "no"}, {"false", "no"}, {"", "no"}, {"anything else", "no"},
	}
	for i, c := range cases {
		start := fmt.Sprintf("2027-05-%02dT10:00:00Z", i+3)
		email := fmt.Sprintf("n%d@example.com", i)
		rec := bookWithAnswer(t, h, slug, start, email,
			fmt.Sprintf(`[{"question_id":%q,"value":%q}]`, qid, c.sent))
		if rec.Code != http.StatusCreated {
			t.Fatalf("sent %q: %d — %s", c.sent, rec.Code, rec.Body.String())
		}
		var id struct {
			ID string `json:"id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &id) //nolint:errcheck

		var stored string
		if err := database.QueryRow(
			`SELECT value FROM booking_answers WHERE booking_id = ? AND question_id = ?`, id.ID, qid).
			Scan(&stored); err != nil {
			t.Fatalf("sent %q: read back: %v", c.sent, err)
		}
		if stored != c.want {
			t.Errorf("sent %q -> stored %q; want %q", c.sent, stored, c.want)
		}
	}
}

// ownerIDOf returns the sole workspace user's id.
func ownerIDOf(t *testing.T, database *sql.DB) string {
	t.Helper()
	var id string
	if err := database.QueryRow(`SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("owner id: %v", err)
	}
	return id
}
