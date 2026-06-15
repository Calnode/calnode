package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/uid"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setup bootstraps the workspace and returns (handler, plainAPIKey, userID).
func setupWorkspace(t *testing.T) (*handler.Handler, string, string) {
	t.Helper()
	h := newTestHandler(t)

	body := `{"name":"Test User","email":"test@example.com","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Setup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: got %d — %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		APIKey string `json:"api_key"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("setup: decode response: %v", err)
	}
	return h, resp.APIKey, resp.UserID
}

func authReq(method, path, body, apiKey string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("X-API-Key", apiKey)
	return r
}

// seedEventType creates an event type via the HTTP handler.
func seedEventTypeHTTP(t *testing.T, h *handler.Handler, apiKey string) (slug, id string) {
	t.Helper()
	slug = "test-meeting-" + uid.New()[:8]
	body := fmt.Sprintf(`{"slug":%q,"name":"Test Meeting","duration_minutes":30}`, slug)
	req := authReq(http.MethodPost, "/v1/event-types", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed event type: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return slug, resp.ID
}


// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func TestSetup_success(t *testing.T) {
	h := newTestHandler(t)
	body := `{"name":"Alice","email":"alice@example.com","timezone":"Pacific/Auckland"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Setup(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.HasPrefix(resp["api_key"], "cno_") {
		t.Errorf("api_key = %q; want cno_ prefix", resp["api_key"])
	}
	if resp["user_id"] == "" {
		t.Error("user_id is empty")
	}
}

func TestSetup_alreadyConfigured(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	body := `{"name":"Bob","email":"bob@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.Setup(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d; want 409", rec.Code)
	}
}

func TestSetup_missingFields(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/setup",
		strings.NewReader(`{"name":"Alice"}`))
	rec := httptest.NewRecorder()
	h.Setup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Auth middleware
// ---------------------------------------------------------------------------

func TestGetMe_returnsSetupUser(t *testing.T) {
	h := newTestHandler(t)
	body := `{"name":"Alice Tester","email":"alice@example.com","timezone":"Pacific/Auckland"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Setup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d — %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		APIKey string `json:"api_key"`
	}
	json.Unmarshal(rec.Body.Bytes(), &setup)

	req2 := authReq(http.MethodGet, "/v1/users/me", "", setup.APIKey)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("get me: %d — %s", rec2.Code, rec2.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &me)
	if me["email"] != "alice@example.com" {
		t.Errorf("email = %v; want alice@example.com", me["email"])
	}
	if me["name"] != "Alice Tester" {
		t.Errorf("name = %v; want Alice Tester", me["name"])
	}
	if me["timezone"] != "Pacific/Auckland" {
		t.Errorf("timezone = %v; want Pacific/Auckland", me["timezone"])
	}
	if me["is_admin"] != true {
		t.Errorf("is_admin = %v; want true", me["is_admin"])
	}
}

func TestRequireAuth_noKey(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	rec := httptest.NewRecorder()

	h.RequireAuth(h.GetMe)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

func TestRequireAuth_invalidKey(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	req := authReq(http.MethodGet, "/v1/users/me", "", "cno_notavalidkey")
	rec := httptest.NewRecorder()

	h.RequireAuth(h.GetMe)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

func TestRequireAuth_validKey(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	req := authReq(http.MethodGet, "/v1/users/me", "", key)
	rec := httptest.NewRecorder()

	h.RequireAuth(h.GetMe)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuth_bearerToken(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()

	h.RequireAuth(h.GetMe)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Event types
// ---------------------------------------------------------------------------

func TestCreateEventType_success(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	body := `{"slug":"30-min-call","name":"30-Minute Call","duration_minutes":30,"routing_mode":"fixed"}`
	req := authReq(http.MethodPost, "/v1/event-types", body, key)
	rec := httptest.NewRecorder()

	h.RequireAuth(h.CreateEventType)(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var et map[string]any
	json.Unmarshal(rec.Body.Bytes(), &et)
	if et["slug"] != "30-min-call" {
		t.Errorf("slug = %v; want 30-min-call", et["slug"])
	}
	if et["duration_minutes"].(float64) != 30 {
		t.Errorf("duration_minutes = %v; want 30", et["duration_minutes"])
	}
}

func TestCreateEventType_duplicateSlug(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	body := `{"slug":"my-slug","name":"First","duration_minutes":30}`

	for _, wantCode := range []int{http.StatusCreated, http.StatusConflict} {
		req := authReq(http.MethodPost, "/v1/event-types", body, key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, req)
		if rec.Code != wantCode {
			t.Errorf("attempt: status = %d; want %d — %s", rec.Code, wantCode, rec.Body.String())
		}
	}
}

func TestListEventTypes(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	for _, slug := range []string{"et-a", "et-b", "et-c"} {
		body := fmt.Sprintf(`{"slug":%q,"name":"Meeting","duration_minutes":30}`, slug)
		req := authReq(http.MethodPost, "/v1/event-types", body, key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", slug, rec.Code)
		}
	}

	req := authReq(http.MethodGet, "/v1/event-types", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 3 {
		t.Errorf("len(items) = %d; want 3", len(resp.Items))
	}
}

func TestGetEventType_notFound(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	req := authReq(http.MethodGet, "/v1/event-types/nonexistent", "", key)
	req.SetPathValue("slug", "nonexistent")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetEventType)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

func TestPatchEventType(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPatch, "/v1/event-types/"+slug,
		`{"name":"Updated Name","duration_minutes":45}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d — %s", rec.Code, rec.Body.String())
	}
	var et map[string]any
	json.Unmarshal(rec.Body.Bytes(), &et)
	if et["name"] != "Updated Name" {
		t.Errorf("name = %v; want Updated Name", et["name"])
	}
	if et["duration_minutes"].(float64) != 45 {
		t.Errorf("duration_minutes = %v; want 45", et["duration_minutes"])
	}
}

func TestPatchEventType_emptyNameRejected(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"name":""}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for empty name", rec.Code)
	}
}

func TestDeleteEventType(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodDelete, "/v1/event-types/"+slug, "", key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteEventType)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d — %s", rec.Code, rec.Body.String())
	}

	// Confirm it's gone.
	req2 := authReq(http.MethodGet, "/v1/event-types/"+slug, "", key)
	req2.SetPathValue("slug", slug)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.GetEventType)(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d; want 404", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// Availability rules
// ---------------------------------------------------------------------------

func TestCreateAndListAvailabilityRule(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	// Create Mon 09:00-17:00.
	body := `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`
	req := authReq(http.MethodPost, "/v1/availability-rules", body, key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d — %s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	ruleID, _ := created["id"].(string)
	if ruleID == "" {
		t.Fatal("rule id is empty")
	}

	// List.
	req2 := authReq(http.MethodGet, "/v1/availability-rules", "", key)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.ListAvailabilityRules)(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list rules: %d", rec2.Code)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(rec2.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Errorf("len(items) = %d; want 1", len(list.Items))
	}

	// Delete.
	req3 := authReq(http.MethodDelete, "/v1/availability-rules/"+ruleID, "", key)
	req3.SetPathValue("id", ruleID)
	rec3 := httptest.NewRecorder()
	h.RequireAuth(h.DeleteAvailabilityRule)(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("delete rule: %d — %s", rec3.Code, rec3.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Slots
// ---------------------------------------------------------------------------

func TestGetSlots_noRules_returnsEmpty(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// No availability rules set → no slots.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/"+slug+"/slots?from=2026-06-15&to=2026-06-15&tz=UTC", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("slots: %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Slots []any `json:"slots"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Slots) != 0 {
		t.Errorf("slots count = %d; want 0 (no rules set)", len(resp.Slots))
	}
}

func TestGetSlots_withRules_returnsSlots(t *testing.T) {
	h, key, userID := setupWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)

	// Get the underlying DB via a round-trip. We need direct DB access to seed
	// availability rules (the HTTP seed helper uses the DB). Use newTestHandler's
	// DB by creating a parallel handler and borrowing its DB from the booking path.
	// Simpler: create the rule through the HTTP endpoint.
	body := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`, etID)
	ruleReq := authReq(http.MethodPost, "/v1/availability-rules", body, key)
	ruleRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(ruleRec, ruleReq)
	if ruleRec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d — %s", ruleRec.Code, ruleRec.Body.String())
	}

	monday := nextMonday()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/"+slug+"/slots?from="+monday+"&to="+monday+"&tz=UTC", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("slots: %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Slots []slotResp `json:"slots"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Slots) == 0 {
		t.Errorf("slots: got 0; want >0 (rule set for Monday)")
	}
	// Verify first slot starts at 09:00 UTC.
	if len(resp.Slots) > 0 {
		start, err := time.Parse(time.RFC3339, resp.Slots[0].Start)
		if err != nil {
			t.Fatalf("parse first slot start: %v", err)
		}
		if start.UTC().Hour() != 9 || start.UTC().Minute() != 0 {
			t.Errorf("first slot = %v; want 09:00 UTC", start)
		}
		// Each slot should carry the host's user ID.
		if len(resp.Slots[0].HostIDs) != 1 || resp.Slots[0].HostIDs[0] != userID {
			t.Errorf("host_ids = %v; want [%s]", resp.Slots[0].HostIDs, userID)
		}
	}
}

func TestGetSlots_notFound(t *testing.T) {
	h, _, _ := setupWorkspace(t)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/does-not-exist/slots?from=2026-06-15&to=2026-06-15", nil)
	req.SetPathValue("slug", "does-not-exist")
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

func TestGetSlots_invalidTZ(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/"+slug+"/slots?tz=Not/ATimezone", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

type slotResp struct {
	Start   string   `json:"start"`
	End     string   `json:"end"`
	HostIDs []string `json:"host_ids"`
}

// ---------------------------------------------------------------------------
// Bookings
// ---------------------------------------------------------------------------

func TestCreateBooking_success(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)

	// Add availability rule.
	body := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`, etID)
	ruleReq := authReq(http.MethodPost, "/v1/availability-rules", body, key)
	ruleRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(ruleRec, ruleReq)
	if ruleRec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d", ruleRec.Code)
	}

	bookBody := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Alice","email":"alice@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(bookBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d — %s", rec.Code, rec.Body.String())
	}
	var b map[string]any
	json.Unmarshal(rec.Body.Bytes(), &b)
	if b["status"] != "confirmed" {
		t.Errorf("status = %v; want confirmed", b["status"])
	}
	if b["id"] == "" {
		t.Error("id is empty")
	}
}

func TestCreateBooking_doubleBooked(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	create := func() int {
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Alice","email":"alice@example.com"}`, slug)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		return rec.Code
	}

	if got := create(); got != http.StatusCreated {
		t.Fatalf("first booking: %d; want 201", got)
	}
	if got := create(); got != http.StatusConflict {
		t.Errorf("second booking: %d; want 409", got)
	}
}

func TestGetBooking_public(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// Create.
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T10:00:00Z","name":"Bob","email":"bob@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	bookingID := created["id"].(string)

	// Get without auth key.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/bookings/"+bookingID, nil)
	req2.SetPathValue("id", bookingID)
	rec2 := httptest.NewRecorder()
	h.GetBooking(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("get booking: %d — %s", rec2.Code, rec2.Body.String())
	}
	var b map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &b)
	if b["id"] != bookingID {
		t.Errorf("id = %v; want %s", b["id"], bookingID)
	}
}

func TestCancelBooking(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// Create booking.
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T11:00:00Z","name":"Carol","email":"carol@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	bookingID := created["id"].(string)

	// Cancel.
	cancelReq := authReq(http.MethodPost, "/v1/bookings/"+bookingID+"/cancel",
		`{"reason":"can't make it"}`, key)
	cancelReq.SetPathValue("id", bookingID)
	cancelRec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel: %d — %s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelled map[string]any
	json.Unmarshal(cancelRec.Body.Bytes(), &cancelled)
	if cancelled["status"] != "cancelled" {
		t.Errorf("status = %v; want cancelled", cancelled["status"])
	}
}

func TestListBookings(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// Create two bookings.
	for _, startAt := range []string{"2026-06-15T09:00:00Z", "2026-06-15T10:00:00Z"} {
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Alice","email":"alice@example.com"}`, slug, startAt)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d — %s", rec.Code, rec.Body.String())
		}
	}

	req := authReq(http.MethodGet, "/v1/bookings", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 2 {
		t.Errorf("len(items) = %d; want 2", len(resp.Items))
	}
}

func TestListBookings_includesAttendeeAndSlug(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T09:00:00Z","name":"Alice Smith","email":"alice@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d — %s", rec.Code, rec.Body.String())
	}

	listReq := authReq(http.MethodGet, "/v1/bookings", "", key)
	listRec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list bookings: %d — %s", listRec.Code, listRec.Body.String())
	}

	var listResp struct {
		Items []struct {
			EventTypeSlug string `json:"event_type_slug"`
			Attendees     []struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"attendees"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("want 1 item; got %d", len(listResp.Items))
	}
	b := listResp.Items[0]
	if b.EventTypeSlug != slug {
		t.Errorf("event_type_slug = %q; want %q", b.EventTypeSlug, slug)
	}
	if len(b.Attendees) != 1 {
		t.Fatalf("want 1 attendee; got %d", len(b.Attendees))
	}
	if b.Attendees[0].Name != "Alice Smith" {
		t.Errorf("attendee name = %q; want Alice Smith", b.Attendees[0].Name)
	}
	if b.Attendees[0].Email != "alice@example.com" {
		t.Errorf("attendee email = %q; want alice@example.com", b.Attendees[0].Email)
	}
}

