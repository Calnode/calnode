package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// addQuestion creates one intake question on an event type via the admin API.
func addQuestion(t *testing.T, h *handler.Handler, apiKey, slug, body string) {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", body, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create question: %d - %s", rec.Code, rec.Body.String())
	}
}

// TestBookPage_selectQuestionRendersWholePage is the regression test for a bug that took
// the public booking page down entirely.
//
// book.html called {{call .T "choose_option"}} for a select's placeholder option, but that
// sits inside {{range .Questions}} where the dot is the QUESTION, which has no T method.
// html/template does not fail before writing: it streams output and aborts mid-write when
// it hits the bad field, so the response was a TRUNCATED page - status 200, correct
// Content-Type, everything up to the dropdown present, and the calendar, the slot picker
// and every script silently missing. Nothing in the page looked like an error.
//
// The assertions therefore check the page is COMPLETE, not merely that it is 200 and
// mentions the question. A test that only looked for the label would have passed against
// the broken build.
func TestBookPage_selectQuestionRendersWholePage(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	addQuestion(t, h, apiKey, slug, `{
		"label": "How did you hear about us?",
		"type": "select",
		"options": ["Google", "Referral", "Other"],
		"required": false
	}`)

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()

	// The truncation happened inside the <select>, so everything below is what disappeared.
	if !strings.Contains(body, "</html>") {
		t.Fatalf("page is truncated - no closing </html>. Rendering aborted partway, which "+
			"is what a template error does here. Last 200 bytes: %q",
			body[max(0, len(body)-200):])
	}
	if !strings.Contains(body, "<select") {
		t.Error("the dropdown itself is missing")
	}
	if !strings.Contains(body, "Referral") {
		t.Error("select options are missing")
	}
	// The calendar and the submit button live AFTER the questions block, so their presence
	// is what proves the render survived the dropdown.
	for _, marker := range []string{"submit-btn", "tz-select"} {
		if !strings.Contains(body, marker) {
			t.Errorf("%q missing: the page stopped rendering before it", marker)
		}
	}
}

// The same page with no questions at all must keep working - guards against "fixing" this
// by removing the translated placeholder.
func TestBookPage_selectPlaceholderIsTranslated(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	addQuestion(t, h, apiKey, slug, `{
		"label": "Pick one", "type": "select", "options": ["A", "B"], "required": false
	}`)

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug+"?lang=es", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "</html>") {
		t.Fatal("page truncated in Spanish")
	}
	// es.json: choose_option. If the placeholder silently fell back to English the
	// translation plumbing is broken even though the page renders.
	if !strings.Contains(body, "Elige una opción") {
		t.Errorf("select placeholder not translated; expected the Spanish string. "+
			"Body contains 'Choose an option': %v", strings.Contains(body, "Choose an option"))
	}
}
