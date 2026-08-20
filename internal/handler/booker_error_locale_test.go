package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postBookingES books via the public endpoint with language:"es", returning the response.
func postBookingES(t *testing.T, h interface {
	CreateBooking(http.ResponseWriter, *http.Request)
}, slug, startAt, name, email, answersJSON string) *httptest.ResponseRecorder {
	t.Helper()
	answers := ""
	if answersJSON != "" {
		answers = `,"answers":` + answersJSON
	}
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":%q,"email":%q,"timezone":"UTC","language":"es"%s}`,
		slug, startAt, name, email, answers)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	return rec
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
	}
	return e.Error
}

// TestBookerFacingErrors_translated covers the API error messages the booking page and
// embed widget render verbatim in their error slot. These used to be unconditionally
// English even on a fully Spanish booking page. Only booker-reachable errors are
// translated — malformed-request diagnostics stay English on purpose, which the last
// subtest pins so that distinction doesn't erode.
func TestBookerFacingErrors_translated(t *testing.T) {
	t.Run("required intake field", func(t *testing.T) {
		h, apiKey, _ := setupWorkspace(t)
		slug, etID := seedEventTypeHTTP(t, h, apiKey)
		seedFullAvailability(t, h, apiKey)

		qrec := httptest.NewRecorder()
		qreq := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions",
			`{"label":"Empresa","type":"text","required":true}`, apiKey)
		qreq.SetPathValue("slug", slug)
		h.RequireAuth(h.CreateQuestion)(qrec, qreq)
		if qrec.Code != http.StatusCreated {
			t.Fatalf("create question: %d — %s", qrec.Code, qrec.Body.String())
		}
		_ = etID

		rec := postBookingES(t, h, slug, "2027-05-03T10:00:00Z", "Ana", "ana@example.com", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400 — %s", rec.Code, rec.Body.String())
		}
		got := errorOf(t, rec)
		if !strings.Contains(got, "falta el campo obligatorio") {
			t.Errorf("error = %q; want the Spanish required-field message", got)
		}
	})

	t.Run("invalid select option", func(t *testing.T) {
		h, apiKey, _ := setupWorkspace(t)
		slug, _ := seedEventTypeHTTP(t, h, apiKey)
		seedFullAvailability(t, h, apiKey)

		qrec := httptest.NewRecorder()
		qreq := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions",
			`{"label":"Tamaño","type":"select","options":["Pequeño","Grande"],"required":false}`, apiKey)
		qreq.SetPathValue("slug", slug)
		h.RequireAuth(h.CreateQuestion)(qrec, qreq)
		if qrec.Code != http.StatusCreated {
			t.Fatalf("create question: %d — %s", qrec.Code, qrec.Body.String())
		}
		var q struct {
			ID string `json:"id"`
		}
		json.Unmarshal(qrec.Body.Bytes(), &q) //nolint:errcheck

		answers := fmt.Sprintf(`[{"question_id":%q,"value":"Enorme"}]`, q.ID)
		rec := postBookingES(t, h, slug, "2027-05-03T10:00:00Z", "Ana", "ana@example.com", answers)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400 — %s", rec.Code, rec.Body.String())
		}
		got := errorOf(t, rec)
		if !strings.Contains(got, "no es una opción permitida") {
			t.Errorf("error = %q; want the Spanish invalid-option message", got)
		}
	})

	t.Run("booking limit reached", func(t *testing.T) {
		h, apiKey, _ := setupWorkspace(t)
		// max_active_bookings defaults to 1 on a plain create, which is what we want here;
		// seedEventTypeHTTP sets 0 (unlimited), so make our own event type.
		erec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(erec, authReq(http.MethodPost, "/v1/event-types",
			`{"slug":"limited","name":"Limited","duration_minutes":30,"max_future_days":0}`, apiKey))
		if erec.Code != http.StatusCreated {
			t.Fatalf("create event type: %d — %s", erec.Code, erec.Body.String())
		}
		seedFullAvailability(t, h, apiKey)

		if rec := postBookingES(t, h, "limited", "2027-05-03T10:00:00Z", "Ana", "ana@example.com", ""); rec.Code != http.StatusCreated {
			t.Fatalf("first booking: %d — %s", rec.Code, rec.Body.String())
		}
		rec := postBookingES(t, h, "limited", "2027-05-04T10:00:00Z", "Ana", "ana@example.com", "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("second booking: %d; want 422 — %s", rec.Code, rec.Body.String())
		}
		got := errorOf(t, rec)
		if !strings.Contains(got, "número máximo de reservas") {
			t.Errorf("error = %q; want the Spanish booking-limit message", got)
		}
	})

	// Malformed-request diagnostics are deliberately NOT translated: they're reachable
	// only from a broken client, and read as API errors rather than UI copy. Pinning this
	// keeps the boundary explicit — if someone later translates everything wholesale,
	// this fails and prompts the conversation.
	t.Run("malformed request stays English", func(t *testing.T) {
		h, apiKey, _ := setupWorkspace(t)
		slug, _ := seedEventTypeHTTP(t, h, apiKey)

		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"not-a-time","name":"Ana","email":"ana@example.com","timezone":"UTC","language":"es"}`, slug)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400", rec.Code)
		}
		if got := errorOf(t, rec); !strings.Contains(got, "must be RFC3339") {
			t.Errorf("error = %q; want the untranslated RFC3339 diagnostic", got)
		}
	})
}
