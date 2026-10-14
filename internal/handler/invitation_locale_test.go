package handler_test

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/i18n"
)

func assertLocalizedError(t *testing.T, rec *httptest.ResponseRecorder, status int, loc *i18n.Locale, key string) {
	t.Helper()
	response := mustJSON(t, rec, status, "localized error")
	if response["error"] != loc.T(key) {
		t.Fatalf("error=%q, want %q (%s)", response["error"], loc.T(key), loc.Code)
	}
}

func TestInvitationLocalizedTerminalStates(t *testing.T) {
	for _, option := range i18n.SupportedLocales() {
		t.Run(option.Code, func(t *testing.T) {
			loc := i18n.Get(option.Code)
			h, db, key, _, slug, _, day := invitationFixture(t)
			for _, state := range []struct {
				name, mutation, key string
				status              int
			}{
				{"invalid", "", "invitation_invalid", 404},
				{"cancelled", `UPDATE scheduling_invitations SET status='cancelled' WHERE id=?`, "invitation_cancelled", 410},
				{"expired status", `UPDATE scheduling_invitations SET status='expired' WHERE id=?`, "invitation_expired", 410},
				{"expired time", `UPDATE scheduling_invitations SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, "invitation_expired", 410},
				{"unavailable", `UPDATE scheduling_invitations SET status='draft' WHERE id=?`, "invitation_unavailable", 410},
				{"booked", "", "invitation_booked", 410},
			} {
				t.Run(state.name, func(t *testing.T) {
					rec, inv := createInvitation(t, h, key, invitationBody(slug))
					mustStatus(t, rec, 201, "create")
					if state.mutation != "" {
						calendarCheckExec(t, db, state.mutation, inv.ID)
					}
					if state.name == "booked" {
						rec = invitationSubmit(h, inv.Token, day.Add(9*time.Hour))
						id := mustString(t, mustJSON(t, rec, 201, "book"), "id", "book")
						waitInvitationConfirmation(t, db, id)
					}
					if state.name == "invalid" {
						inv.Token = "invalid"
					}
					rec = invitationPublic(h, h.InvitationPage, "GET", inv.Token, "?lang="+loc.Code, "")
					mustStatus(t, rec, state.status, "localized page")
					for _, text := range []string{`<html lang="` + loc.Code + `">`, html.EscapeString(loc.T("invitation_title")), html.EscapeString(loc.T(state.key))} {
						if !strings.Contains(rec.Body.String(), text) {
							t.Errorf("page missing %q", text)
						}
					}
					if len(rec.Result().Cookies()) != 1 || rec.Result().Cookies()[0].Value != loc.Code {
						t.Fatal("terminal page did not persist language selection")
					}
					if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
						t.Fatal("terminal page lost credential headers")
					}
					errorKey := state.key
					if state.name == "invalid" {
						errorKey = "err_invitation_link"
					}
					for _, surface := range []http.HandlerFunc{h.GetPublicInvitationSlots, h.CreateInvitationBooking} {
						rec = invitationPublic(h, surface, "POST", inv.Token, "?lang="+loc.Code, `{}`)
						assertLocalizedError(t, rec, state.status, loc, errorKey)
					}
				})
			}
		})
	}
}

func TestInvitationLocaleSelectionAndPropagation(t *testing.T) {
	h, db, key, _, slug, _, day := invitationFixture(t)
	calendarCheckExec(t, db, `UPDATE server_settings SET fallback_locale='es' WHERE id=1`)
	rec, inv := createInvitation(t, h, key, invitationBody(slug))
	mustStatus(t, rec, 201, "create")
	for _, tc := range []struct{ name, query, cookie, accept, want string }{
		{"query wins", "?lang=fr-CA", "de", "it", "fr-CA"},
		{"cookie wins", "", "de", "it", "de"},
		{"header", "", "", "it", "it"},
		{"fallback", "", "", "ja", "es"},
		{"unsupported query", "?lang=ja", "", "sv", "sv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/s/"+inv.Token+tc.query, nil)
			req.SetPathValue("token", inv.Token)
			req.Header.Set("Accept-Language", tc.accept)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "calnode_lang", Value: tc.cookie})
			}
			rec := httptest.NewRecorder()
			h.InvitationPage(rec, req)
			mustStatus(t, rec, 200, "active page")
			if !strings.Contains(rec.Body.String(), `<html lang="`+tc.want+`">`) || !strings.Contains(rec.Body.String(), `const LOCALE   = "`+tc.want+`"`) {
				t.Fatal("active page lost locale")
			}
			req = req.Clone(req.Context())
			req.URL.RawQuery += "&tz=Invalid/Zone"
			rec = httptest.NewRecorder()
			h.GetPublicInvitationSlots(rec, req)
			assertLocalizedError(t, rec, 400, i18n.Get(tc.want), "err_invalid_timezone")
		})
	}
	body := fmt.Sprintf(`{"start_at":%q,"language":"fr-CA","timezone":"Invalid/Zone"}`, day.Add(9*time.Hour).Format(time.RFC3339))
	rec = invitationPublic(h, h.CreateInvitationBooking, "POST", inv.Token, "?lang=de", body)
	assertLocalizedError(t, rec, 400, i18n.Get("fr-CA"), "err_invalid_timezone")
	body = fmt.Sprintf(`{"start_at":%q,"language":"fr-CA","timezone":"UTC"}`, day.Add(9*time.Hour).Format(time.RFC3339))
	rec = invitationPublic(h, h.CreateInvitationBooking, "POST", inv.Token, "?lang=de", body)
	id := mustString(t, mustJSON(t, rec, 201, "book"), "id", "book")
	waitInvitationConfirmation(t, db, id)
	var stored string
	if err := db.QueryRow(`SELECT locale FROM booking_attendees WHERE booking_id=? AND is_organizer=1`, id).Scan(&stored); err != nil || stored != "fr-CA" {
		t.Fatalf("stored locale=%q err=%v", stored, err)
	}
}

func TestInvitationLocalizedBookingAndManagementErrors(t *testing.T) {
	for _, option := range i18n.SupportedLocales() {
		t.Run(option.Code, func(t *testing.T) {
			loc := i18n.Get(option.Code)
			h, db, key, _, slug, etID, day := invitationFixture(t)
			rec, inv := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, 201, "create")
			for _, tc := range []struct {
				body, key string
				status    int
			}{
				{`{"start_at":"invalid"}`, "err_invitation_submission", 400},
				{fmt.Sprintf(`{"start_at":%q,"recipient":{}}`, day.Add(9*time.Hour).Format(time.RFC3339)), "err_invitation_submission", 400},
				{fmt.Sprintf(`{"start_at":%q,"timezone":"Invalid/Zone"}`, day.Add(9*time.Hour).Format(time.RFC3339)), "err_invalid_timezone", 400},
				{fmt.Sprintf(`{"start_at":%q}`, day.Add(8*time.Hour).Format(time.RFC3339)), "err_invitation_slot", 409},
			} {
				rec = invitationPublic(h, h.CreateInvitationBooking, "POST", inv.Token, "?lang="+loc.Code, tc.body)
				assertLocalizedError(t, rec, tc.status, loc, tc.key)
			}
			calendarCheckExec(t, db, `INSERT INTO event_type_questions (id,event_type_id,label,type,required,position) VALUES ('question',?,'Consent','checkbox',1,0)`, etID)
			body := fmt.Sprintf(`{"start_at":%q,"answers":[]}`, day.Add(9*time.Hour).Format(time.RFC3339))
			rec = invitationPublic(h, h.CreateInvitationBooking, "POST", inv.Token, "?lang="+loc.Code, body)
			response := mustJSON(t, rec, 400, "required answer")
			if response["error"] != fmt.Sprintf(loc.T("err_required_checkbox"), "Consent") {
				t.Fatal("intake error lost locale", response)
			}
			calendarCheckExec(t, db, `DELETE FROM event_type_questions WHERE id='question'`)
			rec = invitationSubmit(h, inv.Token, day.Add(9*time.Hour))
			id := mustString(t, mustJSON(t, rec, 201, "book"), "id", "book")
			waitInvitationConfirmation(t, db, id)
			token, err := booking.New(db).IssueManageToken(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			rec = invitationPublic(h, h.ManagePage, "GET", token, "?lang="+loc.Code, "")
			mustStatus(t, rec, 200, "localized management page")
			if !strings.Contains(rec.Body.String(), `<html lang="`+loc.Code+`">`) {
				t.Fatal("manage page lost locale")
			}
			rec = invitationPublic(h, h.RescheduleByToken, "POST", token, "?lang="+loc.Code, fmt.Sprintf(`{"start_at":%q}`, day.Add(8*time.Hour).Format(time.RFC3339)))
			assertLocalizedError(t, rec, 409, loc, "slot_no_longer_available_error")
			calendarCheckExec(t, db, `UPDATE bookings SET status='cancelled' WHERE id=?`, id)
			rec = invitationPublic(h, h.GetManageInvitationSlots, "GET", token, "?lang="+loc.Code, "")
			assertLocalizedError(t, rec, 410, loc, "err_invitation_booking_cancelled")
			rec = invitationPublic(h, h.CancelByToken, "POST", token, "?lang="+loc.Code, `{}`)
			assertLocalizedError(t, rec, 409, loc, "already_cancelled_error")
			rec = invitationPublic(h, h.GetManageInvitationSlots, "GET", "invalid", "?lang="+loc.Code, "")
			assertLocalizedError(t, rec, 404, loc, "err_invitation_manage_link")
		})
	}
}

func TestDurationPolicyLocalizedFeedback(t *testing.T) {
	for _, option := range i18n.SupportedLocales() {
		t.Run(option.Code, func(t *testing.T) {
			loc := i18n.Get(option.Code)
			h, _, key, _, slug, _, _ := invitationFixture(t)
			for _, tc := range []struct {
				duration int
				key      string
			}{{0, "err_duration_positive"}, {60, "err_duration_fixed"}} {
				body := invitationBody(slug)
				body["duration_minutes"] = tc.duration
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req := authReq("POST", "/v1/scheduling-invitations?lang="+loc.Code, string(raw), key)
				rec := httptest.NewRecorder()
				h.RequireAuth(h.CreateSchedulingInvitation)(rec, req)
				assertLocalizedError(t, rec, 400, loc, tc.key)
			}
			for _, tc := range []struct{ body, key string }{
				{`{"min_duration_minutes":15}`, "err_duration_policy_limits"},
				{`{"min_duration_minutes":15,"max_duration_minutes":60,"duration_increment_minutes":20}`, "err_duration_policy_range"},
			} {
				req := authReq("PATCH", "/v1/event-types/"+slug+"?lang="+loc.Code, tc.body, key)
				req.SetPathValue("slug", slug)
				rec := httptest.NewRecorder()
				h.RequireAuth(h.PatchEventType)(rec, req)
				assertLocalizedError(t, rec, 400, loc, tc.key)
			}
			req := authReq("POST", "/v1/event-types?lang="+loc.Code, `{"slug":"invalid-policy","name":"Policy","duration_minutes":30,"min_duration_minutes":15}`, key)
			rec := httptest.NewRecorder()
			h.RequireAuth(h.CreateEventType)(rec, req)
			assertLocalizedError(t, rec, 400, loc, "err_duration_policy_limits")
		})
	}
}
