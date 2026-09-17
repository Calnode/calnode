package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestCreateBooking_honeypotRejected: a filled honeypot ("company") field marks an
// automated submission and is rejected without creating a booking.
func TestCreateBooking_honeypotRejected(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Bot","email":"bot@example.com","company":"Acme Spam Co"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("honeypot-filled booking: %d; want 400", rec.Code)
	}
}

// autofillTokens is a small subset of the words Chromium classifies a field by
// (components/autofill/core/browser/form_parsing/resources/legacy_regex_patterns.json):
// company, name, email, phone and address, in the languages this project ships. It is
// deliberately a tripwire for the obvious regression, not a copy of that file.
var autofillTokens = regexp.MustCompile(`(?i)compan|business|organi[sz]ation|firma|empresa|soci[eé]t[eé]|azienda|bedrijf|f[öo]retag|name|nom|nombre|e.?mail|courriel|correo|phone|tel|mobile|addr|street|city|zip|postal|country`)

// TestBookPage_honeypotGivesAutofillNothingToClassify: browser autofill fills fields it
// recognises by label and name, and a "Company" label with name="company" made Chrome
// fill the honeypot from a real booker's address profile, so the server rejected people
// as bots (#33). The rendered input must carry no label and no autofill-recognisable
// name or id, while the page still posts it as "company".
func TestBookPage_honeypotGivesAutofillNothingToClassify(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d — %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	const wrapperOpen = `<div aria-hidden="true" style="position:absolute;left:-5000px;`
	start := strings.Index(body, wrapperOpen)
	if start < 0 {
		t.Fatalf("honeypot wrapper not found in the booking page")
	}
	end := strings.Index(body[start:], "</div>")
	if end < 0 {
		t.Fatalf("honeypot wrapper is not closed")
	}
	block := body[start : start+end]

	inputs := regexp.MustCompile(`<input\b[^>]*>`).FindAllString(block, -1)
	if len(inputs) != 1 {
		t.Fatalf("honeypot wrapper holds %d inputs; want exactly 1:\n%s", len(inputs), block)
	}
	if text := strings.TrimSpace(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(block, "")); text != "" {
		t.Errorf("honeypot wrapper contains text %q, which autofill reads as the field's label", text)
	}
	if strings.Contains(block, "<label") {
		t.Errorf("honeypot has a <label>; autofill classifies fields by label text:\n%s", block)
	}

	attrs := map[string]string{}
	for _, m := range regexp.MustCompile(`([a-z-]+)="([^"]*)"`).FindAllStringSubmatch(inputs[0], -1) {
		attrs[m[1]] = m[2]
	}
	if attrs["id"] == "" {
		t.Fatalf("honeypot input has no id for the submit script to read: %s", inputs[0])
	}
	for _, a := range []string{"name", "id"} {
		if autofillTokens.MatchString(attrs[a]) {
			t.Errorf("honeypot %s=%q is a word autofill classifies fields by", a, attrs[a])
		}
	}
	for _, a := range []string{"placeholder", "aria-label", "title", "value"} {
		if v, ok := attrs[a]; ok {
			t.Errorf("honeypot has %s=%q, which autofill can read as a label", a, v)
		}
	}
	if strings.Contains(body, `name="company"`) || strings.Contains(body, `id="f-company"`) {
		t.Error(`the page still renders a field named or id'd "company"`)
	}

	// The API field is unchanged: the submit script must read this input by its id and
	// post it as "company", or the server-side check silently stops seeing bots.
	if want := "company: $('" + attrs["id"] + "').value"; !strings.Contains(body, want) {
		t.Errorf("submit script does not post the honeypot as company; want %q in the page", want)
	}
}

// TestCreateBooking_perEmailThrottle: one email can create up to the hourly cap
// across distinct slots, then is throttled with 429.
func TestCreateBooking_perEmailThrottle(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	book := func(hhmm string) int {
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T%s:00Z","name":"Sam","email":"sam@example.com"}`, slug, hhmm)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		return rec.Code
	}

	// 10 distinct slots (the cap), all same email — all succeed.
	times := []string{"09:00", "09:30", "10:00", "10:30", "11:00", "11:30", "12:00", "12:30", "13:00", "13:30"}
	for i, hhmm := range times {
		if code := book(hhmm); code != http.StatusCreated {
			t.Fatalf("booking %d (%s): %d; want 201", i+1, hhmm, code)
		}
	}
	// 11th distinct slot, same email → over the hourly cap.
	if code := book("14:00"); code != http.StatusTooManyRequests {
		t.Errorf("11th booking from same email: %d; want 429", code)
	}
}
