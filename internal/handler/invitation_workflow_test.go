package handler_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/slots"
)

func invitationPublic(h *handler.Handler, fn http.HandlerFunc, method, token, suffix, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/schedule/"+token+suffix, strings.NewReader(body))
	req.SetPathValue("token", token)
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}
func invitationSubmit(h *handler.Handler, token string, start time.Time) *httptest.ResponseRecorder {
	return invitationPublic(h, h.CreateInvitationBooking, "POST", token, "/book", fmt.Sprintf(`{"start_at":%q,"timezone":"UTC","answers":[]}`, start.Format(time.RFC3339)))
}
func waitInvitationQuery(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := db.QueryRow(query, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("asynchronous booking work did not finish:", query)
}
func waitInvitationConfirmation(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	waitInvitationQuery(t, db, `SELECT COUNT(*) FROM jobs WHERE type='reminder.send' AND payload LIKE ?`, "%"+id+"%")
}

func TestInvitationAPILifecycleAndOwnership(t *testing.T) {
	h, db, key, _, slug, _, _ := invitationFixture(t)
	body := invitationBody(slug)
	body["delivery"] = "calnode"
	rec, _ := createInvitation(t, h, key, body)
	mustStatus(t, rec, 400, "unsupported delivery")
	body = invitationBody(slug)
	rec, inv := createInvitation(t, h, key, body)
	created := mustJSON(t, rec, 201, "create")
	if !strings.HasSuffix(mustString(t, created, "scheduling_url", "create"), "/s/"+inv.Token) {
		t.Fatal("missing scheduling URL")
	}
	calendarCheckExec(t, db, `INSERT INTO users (id,email,name) VALUES ('member','member@example.com','Member')`)
	calendarCheckExec(t, db, `INSERT INTO api_keys (id,user_id,name,key_hash) VALUES ('key','member','test',?)`, sha256HexForTest("member-key"))
	for _, tc := range []struct {
		key   string
		total int
	}{{key, 1}, {"member-key", 0}} {
		r := authReq("GET", "/v1/scheduling-invitations?limit=1&offset=0", "", tc.key)
		rec = httptest.NewRecorder()
		h.RequireAuth(h.ListSchedulingInvitations)(rec, r)
		response := mustJSON(t, rec, 200, "list")
		if response["total"] != float64(tc.total) || strings.Contains(rec.Body.String(), inv.Token) || strings.Contains(rec.Body.String(), "scheduling_url") {
			t.Fatal("unsafe or incorrect list", response)
		}
	}
	for _, fn := range []http.HandlerFunc{h.GetSchedulingInvitation, h.CancelSchedulingInvitation} {
		r := authReq("POST", "/v1/scheduling-invitations/"+inv.ID, "", "member-key")
		r.SetPathValue("id", inv.ID)
		rec = httptest.NewRecorder()
		h.RequireAuth(fn)(rec, r)
		mustStatus(t, rec, 404, "cross-owner access")
	}
	cancel := func() *httptest.ResponseRecorder {
		r := authReq("POST", "/v1/scheduling-invitations/"+inv.ID+"/cancel", "", key)
		r.SetPathValue("id", inv.ID)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CancelSchedulingInvitation)(rec, r)
		return rec
	}
	mustStatus(t, cancel(), 200, "cancel")
	mustStatus(t, cancel(), 200, "idempotent cancel")
	rec = invitationPublic(h, h.InvitationPage, "GET", inv.Token, "", "")
	mustStatus(t, rec, 410, "cancelled page")
	if !strings.Contains(rec.Body.String(), "cancelled") {
		t.Fatal("missing cancelled state")
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_invitation_events WHERE invitation_id=? AND event='scheduling_invitation.cancelled'`, inv.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("cancel events: %d %v", events, err)
	}
	for _, query := range []string{"limit=0", "limit=101", "offset=-1", "limit=x"} {
		rec = httptest.NewRecorder()
		h.RequireAuth(h.ListSchedulingInvitations)(rec, authReq("GET", "/v1/scheduling-invitations?"+query, "", key))
		mustStatus(t, rec, 400, "invalid pagination")
	}
}

func TestInvitationPublicAuthorizationAndStates(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		code           int
	}{
		{"active", "", 200}, {"expired", `UPDATE scheduling_invitations SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, 410},
		{"cancelled", `UPDATE scheduling_invitations SET status='cancelled' WHERE id=?`, 410},
		{"missing hosts", `DELETE FROM scheduling_invitation_hosts WHERE invitation_id=?`, 410},
		{"missing token", `DELETE FROM scheduling_invitation_tokens WHERE invitation_id=?`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, key, _, slug, _, _ := invitationFixture(t)
			rec, inv := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, 201, "create")
			if tc.mutation != "" {
				calendarCheckExec(t, db, tc.mutation, inv.ID)
			}
			rec = invitationPublic(h, h.InvitationPage, "GET", inv.Token, "", "")
			mustStatus(t, rec, tc.code, "page")
			if rec.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
				t.Fatal("unsafe credential page headers")
			}
		})
	}
	h, db, key, owner, slug, _, day := invitationFixture(t)
	body := invitationBody(slug)
	body["external"] = map[string]string{"system": "tickets", "reference": "PRIVATE-TICKET", "url": "https://external.example/ticket"}
	rec, inv := createInvitation(t, h, key, body)
	mustStatus(t, rec, 201, "create")
	calendarCheckExec(t, db, `UPDATE server_settings SET head_html='<script src="https://tracker.example/injected.js"></script>',ga4_measurement_id='G-TEST123',gtm_container_id='GTM-TEST123',datalayer_enabled=1 WHERE id=1`)
	calendarCheckExec(t, db, `UPDATE users SET avatar_url='https://tracker.example/avatar.png' WHERE id=?`, owner)
	rec = invitationPublic(h, h.InvitationPage, "GET", inv.Token, "", "")
	mustStatus(t, rec, 200, "safe active page")
	for _, secret := range []string{"PRIVATE-TICKET", "external.example", "tracker.example", "googletagmanager.com", "https://www.google-analytics.com"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("credential page leaked metadata or remote resources:", secret)
		}
	}
	for _, field := range []string{`"duration_minutes":15`, `"host_id":"someone"`, `"email":"attacker@example.com"`, `"event_type_slug":"different"`, `"available_from":"2000-01-01"`} {
		body := fmt.Sprintf(`{"start_at":%q,"timezone":"UTC",%s}`, day.Add(9*time.Hour).Format(time.RFC3339), field)
		rec = invitationPublic(h, h.CreateInvitationBooking, "POST", inv.Token, "/book", body)
		mustStatus(t, rec, 400, "public tampering")
	}
	mustStatus(t, invitationSubmit(h, "invalid", day.Add(9*time.Hour)), 404, "invalid token")
	mustStatus(t, invitationSubmit(h, inv.Token, day.Add(9*time.Hour+time.Minute)), 409, "off-grid time")
}

func TestInvitationDurationGapsBuffersAndRechecks(t *testing.T) {
	for _, tc := range []struct {
		name          string
		duration      int
		before, after int
		want          int
	}{
		{"long excludes small gap", 60, 0, 0, 409}, {"short fits smaller gap", 15, 0, 0, 201}, {"before buffer", 15, 16, 0, 409}, {"after buffer", 15, 0, 1, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, key, owner, slug, etID, day := invitationFixture(t)
			mustStatus(t, patchInvitationEvent(t, h, slug, key, fmt.Sprintf(`{"min_duration_minutes":15,"max_duration_minutes":120,"duration_increment_minutes":15,"slot_interval_minutes":15,"buffer_before_minutes":%d,"buffer_after_minutes":%d}`, tc.before, tc.after)), 200, "policy")
			start := day.Add(10 * time.Hour)
			calendarCheckExec(t, db, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('before',?,?,?,?,'confirmed'),('after',?,?,?,?,'confirmed')`, etID, owner, start.Add(-time.Hour).Format(time.RFC3339), start.Format(time.RFC3339), etID, owner, start.Add(30*time.Minute).Format(time.RFC3339), start.Add(time.Hour).Format(time.RFC3339))
			calendarCheckExec(t, db, `INSERT INTO booking_hosts (booking_id,user_id,is_primary) VALUES ('before',?,1),('after',?,1)`, owner, owner)
			body := invitationBody(slug)
			body["duration_minutes"] = tc.duration
			rec, inv := createInvitation(t, h, key, body)
			mustStatus(t, rec, 201, "create")
			rec = invitationPublic(h, h.GetPublicInvitationSlots, "GET", inv.Token, "/slots?from="+day.Format("2006-01-02")+"&to="+day.Format("2006-01-02")+"&tz=UTC", "")
			mustStatus(t, rec, 200, "slots")
			var result invitationSlotsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			offered := false
			for _, slot := range result.Slots {
				if slot.Start.Equal(start) {
					offered = true
				}
			}
			if offered != (tc.want == 201) {
				t.Fatalf("slot calculation disagrees: offered=%v", offered)
			}
			rec = invitationSubmit(h, inv.Token, start)
			mustStatus(t, rec, tc.want, "booking")
			if tc.want == 201 {
				waitInvitationConfirmation(t, db, mustString(t, mustJSON(t, rec, 201, "book"), "id", "book"))
			}
		})
	}
	for _, change := range []string{"working hours", "calendar", "provider failure", "event disabled", "host archived"} {
		t.Run(change, func(t *testing.T) {
			h, db, key, owner, slug, etID, day := invitationFixture(t)
			rec, inv := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, 201, "create")
			rec, result := previewInvitation(t, h, key, inv.ID, day)
			mustStatus(t, rec, 200, "preview")
			if len(result.Slots) == 0 {
				t.Fatal("no original slots")
			}
			want := 409
			switch change {
			case "working hours":
				calendarCheckExec(t, db, `DELETE FROM availability_rules WHERE user_id=?`, owner)
			case "calendar", "provider failure":
				provider := &conflictCalendar{name: "google", busy: map[string][]slots.Interval{owner: {{Start: day.Add(9 * time.Hour), End: day.Add(12 * time.Hour)}}}}
				if change == "provider failure" {
					provider.err = fmt.Errorf("private provider failure")
					want = 503
				}
				svc := calendar.NewService(db)
				svc.Register(provider)
				h.SetCalendar(svc)
			case "event disabled":
				calendarCheckExec(t, db, `UPDATE event_types SET is_active=0 WHERE id=?`, etID)
				want = 410
			case "host archived":
				calendarCheckExec(t, db, `UPDATE users SET archived_at='2026-01-01' WHERE id=?`, owner)
				want = 410
			}
			rec = invitationSubmit(h, inv.Token, result.Slots[0].Start)
			mustStatus(t, rec, want, "changed availability")
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE scheduling_invitation_id=?`, inv.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unsafe booking: %d %v", count, err)
			}
		})
	}
}

func TestInvitationConcurrentConsumptionAndCompetingBookings(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprintf("same invitation %v", same), func(t *testing.T) {
			h, db, key, _, slug, _, day := invitationFixture(t)
			rec, first := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, 201, "first")
			second := first
			if !same {
				rec, second = createInvitation(t, h, key, invitationBody(slug))
				mustStatus(t, rec, 201, "second")
			}
			ready := make(chan struct{})
			var wg sync.WaitGroup
			results := make(chan *httptest.ResponseRecorder, 2)
			for _, inv := range []invitationResponse{first, second} {
				wg.Add(1)
				go func(token string) {
					defer wg.Done()
					<-ready
					results <- invitationSubmit(h, token, day.Add(9*time.Hour))
				}(inv.Token)
			}
			close(ready)
			wg.Wait()
			close(results)
			successes := 0
			for rec := range results {
				if rec.Code == 201 {
					successes++
					waitInvitationConfirmation(t, db, mustString(t, mustJSON(t, rec, 201, "book"), "id", "book"))
				} else if rec.Code != 409 && rec.Code != 410 {
					t.Fatalf("unexpected concurrency result %d: %s", rec.Code, rec.Body)
				}
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil || count != 1 || successes != 1 {
				t.Fatalf("bookings=%d successes=%d err=%v", count, successes, err)
			}
			if same {
				mustStatus(t, invitationSubmit(h, first.Token, day.Add(9*time.Hour)), 410, "replay")
			}
		})
	}
}

func TestInvitationRetainsRotationUnlessFixedHostRestricted(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprintf("restricted %v", restricted), func(t *testing.T) {
			h, db, key, owner, slug, etID, day := invitationFixture(t)
			calendarCheckExec(t, db, `INSERT INTO users (id,email,name,iana_timezone) VALUES ('rotation','rotation@example.com','Rotation','UTC')`)
			seedInvitationHours(t, db, "rotation", day, "09:00", "12:00")
			mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"rotation"},{"user_id":"rotation","role":"rotation"}]}`, owner)), 200, "rotation hosts")
			mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"routing_mode":"round_robin"}`), 200, "routing")
			start := day.Add(10 * time.Hour)
			calendarCheckExec(t, db, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('busy',?,?,?,?,'confirmed')`, etID, owner, start.Format(time.RFC3339), start.Add(time.Hour).Format(time.RFC3339))
			calendarCheckExec(t, db, `INSERT INTO booking_hosts (booking_id,user_id,is_primary) VALUES ('busy',?,1)`, owner)
			body := invitationBody(slug)
			if restricted {
				body["host_id"] = owner
			}
			rec, inv := createInvitation(t, h, key, body)
			mustStatus(t, rec, 201, "create")
			rec = invitationPublic(h, h.GetPublicInvitationSlots, "GET", inv.Token, "/slots?from=0001-01-01&to="+day.Format("2006-01-02"), "")
			mustStatus(t, rec, 400, "bound historical slot scans")
			rec = invitationSubmit(h, inv.Token, start)
			if restricted {
				mustStatus(t, rec, 409, "selected busy host never falls back")
				return
			}
			id := mustString(t, mustJSON(t, rec, 201, "normal rotation"), "id", "normal rotation")
			waitInvitationConfirmation(t, db, id)
			var assigned string
			if err := db.QueryRow(`SELECT host_id FROM bookings WHERE id=?`, id).Scan(&assigned); err != nil || assigned != "rotation" {
				t.Fatalf("routing changed %q %v", assigned, err)
			}
		})
	}
}

func TestInvitationBusyRangesIncludeLongBookingsAndBuffers(t *testing.T) {
	for _, tc := range []struct {
		name          string
		startDays     int
		endDays       int
		before, after int
		blockedHour   int
		wantHours     []float64
	}{
		{"booking spanning several days", -5, 0, 0, 0, 9, []float64{10.5, 11, 11.5}},
		{"past appointment with long after buffer", -5, -4, 0, 4 * 24 * 60, 9, []float64{10.5, 11, 11.5}},
		{"future appointment with long before buffer", 4, 4, 4 * 24 * 60, 0, 11, []float64{9, 9.5, 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, key, owner, slug, etID, day := invitationFixture(t)
			mustStatus(t, patchInvitationEvent(t, h, slug, key, fmt.Sprintf(`{"buffer_before_minutes":%d,"buffer_after_minutes":%d}`, tc.before, tc.after)), 200, "buffers")
			start := day.AddDate(0, 0, tc.startDays).Add(10*time.Hour + 30*time.Minute)
			end := day.AddDate(0, 0, tc.endDays).Add(10*time.Hour + 30*time.Minute)
			if tc.startDays == tc.endDays {
				end = end.Add(time.Hour)
			}
			calendarCheckExec(t, db, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('busy',?,?,?,?,'confirmed')`, etID, owner, start.Format(time.RFC3339), end.Format(time.RFC3339))
			calendarCheckExec(t, db, `INSERT INTO booking_hosts (booking_id,user_id,is_primary) VALUES ('busy',?,1)`, owner)
			rec, inv := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, 201, "create")
			rec, result := previewInvitation(t, h, key, inv.ID, day)
			mustStatus(t, rec, 200, "preview")
			if len(result.Slots) != len(tc.wantHours) {
				t.Fatalf("slots=%v; want starts at hours %v", result.Slots, tc.wantHours)
			}
			for i, hour := range tc.wantHours {
				if !result.Slots[i].Start.Equal(day.Add(time.Duration(hour * float64(time.Hour)))) {
					t.Fatalf("unexpected slot %+v", result.Slots[i])
				}
			}
			mustStatus(t, invitationSubmit(h, inv.Token, day.Add(time.Duration(tc.blockedHour)*time.Hour)), 409, "blocked submission")
		})
	}
}
