package handler_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/uid"
)

type invitationResponse struct {
	ID              string              `json:"id"`
	Token           string              `json:"token"`
	DurationMinutes int                 `json:"duration_minutes"`
	RoutingMode     string              `json:"routing_mode"`
	Hosts           []handler.EventHost `json:"hosts"`
	External        map[string]string   `json:"external"`
	AvailableFrom   time.Time           `json:"available_from"`
	AvailableUntil  time.Time           `json:"available_until"`
}

type invitationSlotsResponse struct {
	Slots []struct {
		Start   time.Time `json:"start"`
		End     time.Time `json:"end"`
		HostIDs []string  `json:"host_ids"`
	} `json:"slots"`
}

func invitationFixture(t *testing.T) (*handler.Handler, *sql.DB, string, string, string, string, time.Time) {
	t.Helper()
	h, database, key, owner := setupWorkspaceWithDB(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	if _, err := database.Exec(`DELETE FROM availability_rules WHERE user_id = ?`, owner); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 2)
	seedInvitationHours(t, database, owner, day, "09:00", "12:00")
	return h, database, key, owner, slug, etID, day
}

func seedInvitationHours(t *testing.T, database *sql.DB, user string, day time.Time, start, end string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES (?, ?, ?, ?, ?)`,
		uid.New(), user, day.Weekday(), start, end); err != nil {
		t.Fatal(err)
	}
}

func invitationBody(slug string) map[string]any {
	return map[string]any{"event_type_slug": slug, "recipient": map[string]string{"name": "Customer", "email": "customer@example.com"},
		"expires_at": time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)}
}

func createInvitation(t *testing.T, h *handler.Handler, key string, body map[string]any) (*httptest.ResponseRecorder, invitationResponse) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateSchedulingInvitation)(rec, authReq(http.MethodPost, "/v1/scheduling-invitations", string(data), key))
	var response invitationResponse
	if rec.Code == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return rec, response
}

func patchInvitationEvent(t *testing.T, h *handler.Handler, slug, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, body, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	return rec
}

func previewInvitation(t *testing.T, h *handler.Handler, key, id string, day time.Time) (*httptest.ResponseRecorder, invitationSlotsResponse) {
	t.Helper()
	path := fmt.Sprintf("/v1/scheduling-invitations/%s/slots?from=%s&to=%s&timezone=UTC", id, day.Format("2006-01-02"), day.Format("2006-01-02"))
	req := authReq(http.MethodGet, path, "", key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetSchedulingInvitationSlots)(rec, req)
	var response invitationSlotsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return rec, response
}

func eventSlotsForDay(t *testing.T, h *handler.Handler, slug string, day time.Time) invitationSlotsResponse {
	t.Helper()
	path := fmt.Sprintf("/v1/event-types/%s/slots?from=%s&to=%s&timezone=UTC", slug, day.Format("2006-01-02"), day.Format("2006-01-02"))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)
	mustStatus(t, rec, http.StatusOK, "event slots")
	var response invitationSlotsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestSchedulingInvitationDurationAndEventTypeParity(t *testing.T) {
	h, _, key, owner, slug, _, day := invitationFixture(t)
	before := eventSlotsForDay(t, h, slug, day)
	if len(before.Slots) != 6 {
		t.Fatalf("event slots = %d; want 6", len(before.Slots))
	}
	for _, slot := range before.Slots {
		if slot.End.Sub(slot.Start) != 30*time.Minute || !reflect.DeepEqual(slot.HostIDs, []string{owner}) {
			t.Fatalf("unexpected event slot: %+v", slot)
		}
	}
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"min_duration_minutes":15,"max_duration_minutes":180,"duration_increment_minutes":15}`), http.StatusOK, "duration policy")
	rec, inherited := createInvitation(t, h, key, invitationBody(slug))
	mustStatus(t, rec, http.StatusCreated, "inherited invitation")
	rec, inheritedSlots := previewInvitation(t, h, key, inherited.ID, day)
	mustStatus(t, rec, http.StatusOK, "inherited slots")
	if !reflect.DeepEqual(before, inheritedSlots) {
		t.Fatalf("invitation defaults differ: %+v vs %+v", before, inheritedSlots)
	}
	body := invitationBody(slug)
	body["duration_minutes"] = 90
	rec, overridden := createInvitation(t, h, key, body)
	mustStatus(t, rec, http.StatusCreated, "duration invitation")
	rec, result := previewInvitation(t, h, key, overridden.ID, day)
	mustStatus(t, rec, http.StatusOK, "duration slots")
	if len(result.Slots) != 4 {
		t.Fatalf("90-minute slots = %d; want 4", len(result.Slots))
	}
	for _, slot := range result.Slots {
		if slot.End.Sub(slot.Start) != 90*time.Minute || slot.End.After(day.Add(12*time.Hour)) {
			t.Fatalf("wrong duration/fit: %+v", slot)
		}
	}
	if after := eventSlotsForDay(t, h, slug, day); !reflect.DeepEqual(before, after) {
		t.Fatal("invitation changed normal event-type slots")
	}
}

func TestSchedulingInvitationHostRestrictionAndSnapshot(t *testing.T) {
	h, database, key, owner, slug, etID, day := invitationFixture(t)
	if _, err := database.Exec(`INSERT INTO users (id,email,name,iana_timezone) VALUES ('other','other@example.com','Other','UTC')`); err != nil {
		t.Fatal(err)
	}
	seedInvitationHours(t, database, "other", day, "10:00", "12:00")
	mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"rotation"},{"user_id":"other","role":"rotation"}]}`, owner)), http.StatusOK, "rotation hosts")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"routing_mode":"round_robin","min_duration_minutes":15,"max_duration_minutes":180,"duration_increment_minutes":15}`), http.StatusOK, "rotation template")
	body := invitationBody(slug)
	body["duration_minutes"] = 90
	body["hosts"] = []handler.EventHost{{UserID: "other", Role: "required"}}
	body["available_from"] = day.Add(10 * time.Hour).Format(time.RFC3339)
	body["available_until"] = day.Add(12 * time.Hour).Format(time.RFC3339)
	rec, inv := createInvitation(t, h, key, body)
	mustStatus(t, rec, http.StatusCreated, "fixed restriction")
	if inv.RoutingMode != "fixed" {
		t.Fatalf("routing = %s", inv.RoutingMode)
	}
	rec, before := previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusOK, "fixed preview")
	if len(before.Slots) != 2 {
		t.Fatalf("restricted slots = %d; want 2", len(before.Slots))
	}
	for _, slot := range before.Slots {
		if !reflect.DeepEqual(slot.HostIDs, []string{"other"}) {
			t.Fatalf("wrong host: %+v", slot)
		}
	}
	// Authorized values and host rows remain independent of template edits.
	// Current notice/future policy intersects the snapshot, as required by #92.
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"duration_minutes":60,"slot_interval_minutes":60,"buffer_before_minutes":30,"buffer_after_minutes":30,"max_future_days":4,"routing_mode":"fixed"}`), http.StatusOK, "change template")
	mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"required"}]}`, owner)), http.StatusOK, "replace template hosts")
	rec, after := previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusOK, "snapshot preview")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("snapshot changed: %+v -> %+v", before, after)
	}
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"min_notice_minutes":10000}`), http.StatusOK, "tighten live notice")
	rec, after = previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusOK, "live notice intersection")
	if len(after.Slots) != 0 {
		t.Fatal("invitation bypassed current minimum notice")
	}
	if normal := eventSlotsForDay(t, h, slug, day); len(normal.Slots) != 0 {
		t.Fatal("normal slots ignored updated notice/future policy")
	}
	if _, err := database.Exec(`UPDATE event_types SET is_active = 0 WHERE id = ?`, etID); err != nil {
		t.Fatal(err)
	}
	rec, _ = previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusConflict, "inactive source")
}

func TestSchedulingInvitationWindowConstrainsWholeAppointment(t *testing.T) {
	h, _, key, _, slug, _, day := invitationFixture(t)
	body := invitationBody(slug)
	body["available_from"] = day.Add(9*time.Hour + 15*time.Minute).Format(time.RFC3339)
	body["available_until"] = day.Add(10*time.Hour + 15*time.Minute).Format(time.RFC3339)
	rec, inv := createInvitation(t, h, key, body)
	mustStatus(t, rec, http.StatusCreated, "window invitation")
	rec, result := previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusOK, "window slots")
	if len(result.Slots) != 1 || !result.Slots[0].Start.Equal(day.Add(9*time.Hour+30*time.Minute)) || !result.Slots[0].End.Equal(day.Add(10*time.Hour)) {
		t.Fatalf("unexpected window slots: %+v", result)
	}
	rec, result = previewInvitation(t, h, key, inv.ID, day.AddDate(0, 0, 1))
	mustStatus(t, rec, http.StatusOK, "outside window")
	if len(result.Slots) != 0 {
		t.Fatal("slots outside window")
	}
}

func TestSchedulingInvitationTokenAndExternalMetadata(t *testing.T) {
	h, database, key, _, slug, _, _ := invitationFixture(t)
	body := invitationBody(slug)
	body["external"] = map[string]string{"system": "crm", "reference": "123", "url": "https://crm.example.com/123"}
	rec, inv := createInvitation(t, h, key, body)
	mustStatus(t, rec, http.StatusCreated, "invitation")
	if rec.Header().Get("Cache-Control") != "no-store" || len(inv.Token) != 64 {
		t.Fatal("credential response must be uncached and opaque")
	}
	var hash, expires string
	var consumed sql.NullString
	if err := database.QueryRow(`SELECT token_hash, expires_at, consumed_at FROM scheduling_invitation_tokens WHERE invitation_id = ?`, inv.ID).Scan(&hash, &expires, &consumed); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(inv.Token))
	if hash != hex.EncodeToString(sum[:]) || consumed.Valid || expires != body["expires_at"] {
		t.Fatal("token was not stored as an expiring, unused hash")
	}
	req := authReq(http.MethodGet, "/v1/scheduling-invitations/"+inv.ID, "", key)
	req.SetPathValue("id", inv.ID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.GetSchedulingInvitation)(rec, req)
	mustStatus(t, rec, http.StatusOK, "read invitation")
	var read invitationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Token != "" || !reflect.DeepEqual(inv.External, read.External) {
		t.Fatal("read exposed token or lost external metadata")
	}
}

func TestSchedulingInvitationRejectsInvalidCreation(t *testing.T) {
	h, database, key, owner, slug, _, _ := invitationFixture(t)
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"fixed duration", "duration_minutes", 60}, {"zero duration", "duration_minutes", 0},
		{"no hosts", "hosts", []handler.EventHost{}},
		{"all optional", "hosts", []handler.EventHost{{UserID: owner, Role: "optional"}}},
		{"duplicate", "hosts", []handler.EventHost{{UserID: owner, Role: "required"}, {UserID: owner, Role: "rotation"}}},
		{"ineligible", "hosts", []handler.EventHost{{UserID: "missing", Role: "required"}}},
		{"bad role", "hosts", []handler.EventHost{{UserID: owner, Role: "bogus"}}},
		{"missing expiry", "expires_at", "0001-01-01T00:00:00Z"},
		{"expired", "expires_at", time.Now().Add(-time.Hour).Format(time.RFC3339)},
		{"recipient", "recipient", map[string]string{"email": "invalid"}},
		{"timezone", "availability_timezone", "Not/AZone"},
		{"malformed bound", "available_from", "tomorrow"},
		{"delivery", "delivery", "smtp"},
		{"fixed host outside scope", "host_id", "missing"},
		{"local timezone alias", "availability_timezone", "Local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := invitationBody(slug)
			body[tc.field] = tc.value
			rec, _ := createInvitation(t, h, key, body)
			mustStatus(t, rec, http.StatusBadRequest, tc.name)
		})
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM scheduling_invitations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid requests left partial invitations")
	}
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"min_duration_minutes":15,"max_duration_minutes":120,"duration_increment_minutes":15}`), http.StatusOK, "range")
	for _, duration := range []int{14, 31, 121} {
		body := invitationBody(slug)
		body["duration_minutes"] = duration
		rec, _ := createInvitation(t, h, key, body)
		mustStatus(t, rec, http.StatusBadRequest, "out-of-policy duration")
	}
}

func TestSchedulingInvitationUnavailableState(t *testing.T) {
	for _, tc := range []struct{ name, mutation string }{
		{"cancelled", `UPDATE scheduling_invitations SET status = 'cancelled' WHERE id = ?`},
		{"draft", `UPDATE scheduling_invitations SET status = 'draft' WHERE id = ?`},
		{"expired", `UPDATE scheduling_invitations SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`},
		{"invalid expiry", `UPDATE scheduling_invitations SET expires_at = 'invalid' WHERE id = ?`},
		{"missing hosts", `DELETE FROM scheduling_invitation_hosts WHERE invitation_id = ?`},
		{"missing token", `DELETE FROM scheduling_invitation_tokens WHERE invitation_id = ?`},
		{"consumed token", `UPDATE scheduling_invitation_tokens SET consumed_at = '2026-01-01T00:00:00Z' WHERE invitation_id = ?`},
		{"expired token", `UPDATE scheduling_invitation_tokens SET expires_at = '2000-01-01T00:00:00Z' WHERE invitation_id = ?`},
		{"invalid window", `UPDATE scheduling_invitations SET available_from = '2099-01-02T00:00:00Z', available_until = '2099-01-01T00:00:00Z' WHERE id = ?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, _, slug, _, day := invitationFixture(t)
			rec, inv := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, http.StatusCreated, "invitation")
			if _, err := database.Exec(tc.mutation, inv.ID); err != nil {
				t.Fatal(err)
			}
			rec, _ = previewInvitation(t, h, key, inv.ID, day)
			mustStatus(t, rec, http.StatusConflict, tc.name)
		})
	}
	h, database, key, owner, slug, _, day := invitationFixture(t)
	rec, inv := createInvitation(t, h, key, invitationBody(slug))
	mustStatus(t, rec, http.StatusCreated, "invitation")
	if _, err := database.Exec(`UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = ?`, owner); err != nil {
		t.Fatal(err)
	}
	// The owner cannot authenticate once archived; a separate required host case
	// is exercised below so it reaches scheduling validation rather than auth.
	rec, _ = previewInvitation(t, h, key, inv.ID, day)
	if rec.Code == http.StatusOK {
		t.Fatal("archived issuer remained authorized")
	}
}

func TestSchedulingInvitationAuthorization(t *testing.T) {
	h, database, key, _, slug, _, day := invitationFixture(t)
	rec, inv := createInvitation(t, h, key, invitationBody(slug))
	mustStatus(t, rec, http.StatusCreated, "invitation")
	for _, badKey := range []string{"", "invalid"} {
		rec, _ = previewInvitation(t, h, badKey, inv.ID, day)
		mustStatus(t, rec, http.StatusUnauthorized, "unauthenticated preview")
	}
	const otherKey = "invitation-other-key"
	if _, err := database.Exec(`INSERT INTO users (id,email,name,iana_timezone) VALUES ('member','member@example.com','Member','UTC')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash) VALUES ('member-key','member','test',?)`, sha256HexForTest(otherKey)); err != nil {
		t.Fatal(err)
	}
	rec, _ = previewInvitation(t, h, otherKey, inv.ID, day)
	mustStatus(t, rec, http.StatusNotFound, "another member's invitation")
	rec, _ = createInvitation(t, h, otherKey, invitationBody(slug))
	mustStatus(t, rec, http.StatusNotFound, "another member's event type")
}

func TestSchedulingInvitationArchivedRequiredHost(t *testing.T) {
	h, database, key, owner, slug, _, day := invitationFixture(t)
	if _, err := database.Exec(`INSERT INTO users (id,email,name,iana_timezone) VALUES ('required','required@example.com','Required','UTC')`); err != nil {
		t.Fatal(err)
	}
	seedInvitationHours(t, database, "required", day, "09:00", "12:00")
	mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"required"},{"user_id":"required","role":"required"}]}`, owner)), http.StatusOK, "required hosts")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"routing_mode":"collective"}`), http.StatusOK, "collective")
	rec, inv := createInvitation(t, h, key, invitationBody(slug))
	mustStatus(t, rec, http.StatusCreated, "invitation")
	deleteReq := authReq(http.MethodDelete, "/v1/users/required", "", key)
	deleteReq.SetPathValue("id", "required")
	deleteRec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(deleteRec, deleteReq)
	mustStatus(t, deleteRec, http.StatusConflict, "retain invitation host")
	deleteReq = authReq(http.MethodDelete, "/v1/event-types/"+slug, "", key)
	deleteReq.SetPathValue("slug", slug)
	deleteRec = httptest.NewRecorder()
	h.RequireAuth(h.DeleteEventType)(deleteRec, deleteReq)
	mustStatus(t, deleteRec, http.StatusConflict, "retain invitation template")
	if _, err := database.Exec(`UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = 'required'`); err != nil {
		t.Fatal(err)
	}
	rec, _ = previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusConflict, "archived required host")
}

func TestSchedulingInvitationDurationFitsBusyIntervals(t *testing.T) {
	h, database, key, owner, slug, etID, day := invitationFixture(t)
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"min_duration_minutes":15,"max_duration_minutes":180,"duration_increment_minutes":15}`), http.StatusOK, "duration policy")
	body := invitationBody(slug)
	body["duration_minutes"] = 90
	rec, inv := createInvitation(t, h, key, body)
	mustStatus(t, rec, http.StatusCreated, "invitation")
	if _, err := database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('busy',?,?,?,?,'confirmed')`,
		etID, owner, day.Add(10*time.Hour).Format(time.RFC3339), day.Add(10*time.Hour+30*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary) VALUES ('busy-host','busy',?,1)`, owner); err != nil {
		t.Fatal(err)
	}
	rec, result := previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusOK, "busy slots")
	if len(result.Slots) != 1 || !result.Slots[0].Start.Equal(day.Add(10*time.Hour+30*time.Minute)) {
		t.Fatalf("appointment did not fit busy intervals: %+v", result)
	}
	if len(eventSlotsForDay(t, h, slug, day).Slots) != 5 {
		t.Fatal("ordinary bookings lost their default duration")
	}
}

func TestSchedulingInvitationInheritedDurationRemainsStable(t *testing.T) {
	h, _, key, _, slug, _, day := invitationFixture(t)
	rec, inv := createInvitation(t, h, key, invitationBody(slug))
	mustStatus(t, rec, http.StatusCreated, "invitation")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"duration_minutes":60}`), http.StatusOK, "default change")
	rec, result := previewInvitation(t, h, key, inv.ID, day)
	mustStatus(t, rec, http.StatusOK, "snapshot")
	if len(result.Slots) != 6 {
		t.Fatal("inherited duration changed after issuance")
	}
	for _, slot := range result.Slots {
		if slot.End.Sub(slot.Start) != 30*time.Minute {
			t.Fatal("inherited duration is not a snapshot")
		}
	}
	if len(eventSlotsForDay(t, h, slug, day).Slots) != 5 {
		t.Fatal("ordinary event type ignored new 60-minute duration")
	}
}

func TestSchedulingInvitationRoleCombinations(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		count      int
	}{
		{"required plus rotation", "rotation", 4},
		{"all required", "required", 4},
		{"optional does not gate", "optional", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, owner, slug, _, day := invitationFixture(t)
			if _, err := database.Exec(`INSERT INTO users (id,email,name,iana_timezone) VALUES ('second','second@example.com','Second','UTC')`); err != nil {
				t.Fatal(err)
			}
			seedInvitationHours(t, database, "second", day, "10:00", "12:00")
			mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"required"},{"user_id":"second","role":"optional"}]}`, owner)), http.StatusOK, "eligible hosts")
			body := invitationBody(slug)
			body["hosts"] = []handler.EventHost{{UserID: owner, Role: "required"}, {UserID: "second", Role: tc.role}}
			rec, inv := createInvitation(t, h, key, body)
			mustStatus(t, rec, http.StatusCreated, tc.name)
			rec, result := previewInvitation(t, h, key, inv.ID, day)
			mustStatus(t, rec, http.StatusOK, "preview")
			if len(result.Slots) != tc.count {
				t.Fatalf("slots = %d; want %d", len(result.Slots), tc.count)
			}
			for _, slot := range result.Slots {
				wantHosts := 2
				if tc.role == "optional" {
					wantHosts = 1
				}
				if len(slot.HostIDs) != wantHosts {
					t.Fatalf("hosts = %+v", slot.HostIDs)
				}
			}
		})
	}
}
