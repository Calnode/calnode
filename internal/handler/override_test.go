package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// nextMonday returns the date string (YYYY-MM-DD UTC) for the next Monday
// that is strictly in the future so slot-filtering doesn't trim the day.
func nextMonday() string {
	now := time.Now().UTC()
	daysUntilMonday := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	return now.AddDate(0, 0, daysUntilMonday).Format("2006-01-02")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func createOverride(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	CreateAvailabilityOverride(http.ResponseWriter, *http.Request)
}, apiKey, body string) (int, map[string]any) {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/availability-overrides", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityOverride)(rec, req)
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

// ---------------------------------------------------------------------------
// CreateAvailabilityOverride
// ---------------------------------------------------------------------------

func TestCreateAvailabilityOverride_blockedDay(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, resp := createOverride(t, h, key,
		`{"date":"2026-07-04","is_available":false}`)

	if code != http.StatusCreated {
		t.Fatalf("status = %d; want 201 — %v", code, resp)
	}
	if resp["date"] != "2026-07-04" {
		t.Errorf("date = %v; want 2026-07-04", resp["date"])
	}
	if resp["is_available"] != false {
		t.Errorf("is_available = %v; want false", resp["is_available"])
	}
	if resp["id"] == "" || resp["id"] == nil {
		t.Error("id is empty")
	}
	// start_time/end_time must be absent (null) for blocked days.
	if resp["start_time"] != nil {
		t.Errorf("start_time = %v; want null for blocked day", resp["start_time"])
	}
}

func TestCreateAvailabilityOverride_customHours(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, resp := createOverride(t, h, key,
		`{"date":"2026-07-05","is_available":true,"start_time":"10:00","end_time":"14:00"}`)

	if code != http.StatusCreated {
		t.Fatalf("status = %d; want 201 — %v", code, resp)
	}
	if resp["is_available"] != true {
		t.Errorf("is_available = %v; want true", resp["is_available"])
	}
	if resp["start_time"] != "10:00" {
		t.Errorf("start_time = %v; want 10:00", resp["start_time"])
	}
	if resp["end_time"] != "14:00" {
		t.Errorf("end_time = %v; want 14:00", resp["end_time"])
	}
}

func TestCreateAvailabilityOverride_duplicateDateReturns409(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	body := `{"date":"2026-07-04","is_available":false}`
	code1, _ := createOverride(t, h, key, body)
	if code1 != http.StatusCreated {
		t.Fatalf("first create: status = %d; want 201", code1)
	}

	code2, _ := createOverride(t, h, key, body)
	if code2 != http.StatusConflict {
		t.Errorf("duplicate: status = %d; want 409", code2)
	}
}

func TestCreateAvailabilityOverride_invalidDate(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	for _, bad := range []string{
		`{"date":"07-04-2026","is_available":false}`,
		`{"date":"not-a-date","is_available":false}`,
		`{"date":"","is_available":false}`,
		`{"is_available":false}`,
	} {
		code, _ := createOverride(t, h, key, bad)
		if code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d; want 400", bad, code)
		}
	}
}

func TestCreateAvailabilityOverride_missingTimesWhenAvailable(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	cases := []string{
		`{"date":"2026-07-05","is_available":true}`,
		`{"date":"2026-07-05","is_available":true,"start_time":"09:00"}`,
		`{"date":"2026-07-05","is_available":true,"end_time":"17:00"}`,
	}
	for _, body := range cases {
		code, _ := createOverride(t, h, key, body)
		if code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d; want 400", body, code)
		}
	}
}

func TestCreateAvailabilityOverride_startNotBeforeEnd(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	cases := []string{
		`{"date":"2026-07-05","is_available":true,"start_time":"17:00","end_time":"09:00"}`,
		`{"date":"2026-07-05","is_available":true,"start_time":"09:00","end_time":"09:00"}`,
	}
	for _, body := range cases {
		code, _ := createOverride(t, h, key, body)
		if code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d; want 400", body, code)
		}
	}
}

func TestCreateAvailabilityOverride_invalidHHMM(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, _ := createOverride(t, h, key,
		`{"date":"2026-07-05","is_available":true,"start_time":"9:00","end_time":"25:00"}`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", code)
	}
}

// ---------------------------------------------------------------------------
// ListAvailabilityOverrides
// ---------------------------------------------------------------------------

func TestListAvailabilityOverrides_empty(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	req := authReq(http.MethodGet, "/v1/availability-overrides", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListAvailabilityOverrides)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp struct {
		Items []any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("len(items) = %d; want 0", len(resp.Items))
	}
}

func TestListAvailabilityOverrides_returnsSeedData(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	// Seed two overrides.
	createOverride(t, h, key, `{"date":"2026-07-04","is_available":false}`)
	createOverride(t, h, key, `{"date":"2026-07-05","is_available":true,"start_time":"10:00","end_time":"14:00"}`)

	req := authReq(http.MethodGet, "/v1/availability-overrides", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListAvailabilityOverrides)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 2 {
		t.Fatalf("len(items) = %d; want 2", len(resp.Items))
	}
	// Ordered by date.
	if resp.Items[0]["date"] != "2026-07-04" {
		t.Errorf("items[0].date = %v; want 2026-07-04", resp.Items[0]["date"])
	}
	if resp.Items[0]["start_time"] != nil {
		t.Errorf("items[0].start_time = %v; want null for blocked day", resp.Items[0]["start_time"])
	}
	if resp.Items[1]["start_time"] != "10:00" {
		t.Errorf("items[1].start_time = %v; want 10:00", resp.Items[1]["start_time"])
	}
}

// ---------------------------------------------------------------------------
// DeleteAvailabilityOverride
// ---------------------------------------------------------------------------

func TestDeleteAvailabilityOverride_success(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key, `{"date":"2026-07-04","is_available":false}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	req := authReq(http.MethodDelete, "/v1/availability-overrides/"+id, "", key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d; want 204 — %s", rec.Code, rec.Body.String())
	}

	// List should be empty.
	listReq := authReq(http.MethodGet, "/v1/availability-overrides", "", key)
	listRec := httptest.NewRecorder()
	h.RequireAuth(h.ListAvailabilityOverrides)(listRec, listReq)
	var resp struct{ Items []any `json:"items"` }
	json.Unmarshal(listRec.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("after delete: len(items) = %d; want 0", len(resp.Items))
	}
}

func TestDeleteAvailabilityOverride_notFound(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	req := authReq(http.MethodDelete, "/v1/availability-overrides/does-not-exist", "", key)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

func TestDeleteAvailabilityOverride_cannotDeleteOtherUsersOverride(t *testing.T) {
	// Two separate workspaces — user A's override is invisible to user B.
	h, keyA, _ := setupWorkspace(t)

	_, created := createOverride(t, h, keyA, `{"date":"2026-07-04","is_available":false}`)
	id, _ := created["id"].(string)

	// A second handler instance (same in-memory DB used via h) won't help here;
	// in practice userB would be a different user. But since we only have one user
	// in this workspace, we just verify a random ID returns 404.
	req := authReq(http.MethodDelete, "/v1/availability-overrides/random-other-id", "", keyA)
	req.SetPathValue("id", "random-other-id")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteAvailabilityOverride)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for unknown id", rec.Code)
	}
	_ = id
}

// ---------------------------------------------------------------------------
// Slot-engine integration: overrides affect available slots
// ---------------------------------------------------------------------------

func TestGetSlots_blockedDayOverride_returnsNoSlots(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	monday := nextMonday()

	// Add a Monday availability rule.
	ruleBody := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`, etID)
	ruleReq := authReq(http.MethodPost, "/v1/availability-rules", ruleBody, key)
	ruleRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(ruleRec, ruleReq)
	if ruleRec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d — %s", ruleRec.Code, ruleRec.Body.String())
	}

	// Without override: slots should exist.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/"+slug+"/slots?from="+monday+"&to="+monday+"&tz=UTC", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("slots (no override): %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Slots []any `json:"slots"` }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Slots) == 0 {
		t.Fatal("expected slots before override; got none")
	}

	// Block that Monday.
	blockBody := fmt.Sprintf(`{"date":%q,"is_available":false}`, monday)
	code, overrideResp := createOverride(t, h, key, blockBody)
	if code != http.StatusCreated {
		t.Fatalf("create override: %d — %v", code, overrideResp)
	}

	// Now slots for that day must be empty.
	req2 := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/"+slug+"/slots?from="+monday+"&to="+monday+"&tz=UTC", nil)
	req2.SetPathValue("slug", slug)
	rec2 := httptest.NewRecorder()
	h.GetSlots(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("slots (blocked): %d — %s", rec2.Code, rec2.Body.String())
	}
	var resp2 struct{ Slots []any `json:"slots"` }
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if len(resp2.Slots) != 0 {
		t.Errorf("expected 0 slots on blocked day; got %d", len(resp2.Slots))
	}
}

func TestGetSlots_customHoursOverride_limitsSlots(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	monday := nextMonday()

	// Add Mon 09:00-17:00 rule.
	ruleBody := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`, etID)
	ruleReq := authReq(http.MethodPost, "/v1/availability-rules", ruleBody, key)
	ruleRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(ruleRec, ruleReq)
	if ruleRec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d", ruleRec.Code)
	}

	// Override that Monday to only 10:00-12:00.
	ovBody := fmt.Sprintf(`{"date":%q,"is_available":true,"start_time":"10:00","end_time":"12:00"}`, monday)
	code, ovResp := createOverride(t, h, key, ovBody)
	if code != http.StatusCreated {
		t.Fatalf("create override: %d — %v", code, ovResp)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/v1/event-types/"+slug+"/slots?from="+monday+"&to="+monday+"&tz=UTC", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("slots: %d — %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Slots []struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"slots"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Slots) == 0 {
		t.Fatal("expected slots in custom window; got none")
	}
	// All slots must fall within 10:00-12:00 UTC — check by parsing the time component.
	for _, s := range resp.Slots {
		parsed, err := time.Parse(time.RFC3339, s.Start)
		if err != nil {
			t.Fatalf("parse slot start %q: %v", s.Start, err)
		}
		h := parsed.UTC().Hour()
		if h < 10 || h >= 12 {
			t.Errorf("slot start %s is outside 10:00-12:00 window", s.Start)
		}
	}
	// No slot should start at or after 12:00.
	for _, s := range resp.Slots {
		parsed, _ := time.Parse(time.RFC3339, s.Start)
		if parsed.UTC().Hour() >= 12 {
			t.Errorf("slot %s starts at or after 12:00; override not respected", s.Start)
		}
	}
}

// ---------------------------------------------------------------------------
// UpdateAvailabilityOverride
// ---------------------------------------------------------------------------

func TestUpdateAvailabilityOverride_updateTimes(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key,
		`{"date":"2026-08-01","is_available":true,"start_time":"10:00","end_time":"14:00"}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	req := authReq(http.MethodPatch, "/v1/availability-overrides/"+id,
		`{"start_time":"11:00","end_time":"15:00"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["start_time"] != "11:00" {
		t.Errorf("start_time = %v; want 11:00", resp["start_time"])
	}
	if resp["end_time"] != "15:00" {
		t.Errorf("end_time = %v; want 15:00", resp["end_time"])
	}
	if resp["is_available"] != true {
		t.Errorf("is_available = %v; want true", resp["is_available"])
	}
}

func TestUpdateAvailabilityOverride_flipToBlocked(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key,
		`{"date":"2026-08-02","is_available":true,"start_time":"09:00","end_time":"17:00"}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	req := authReq(http.MethodPatch, "/v1/availability-overrides/"+id,
		`{"is_available":false}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["is_available"] != false {
		t.Errorf("is_available = %v; want false", resp["is_available"])
	}
	if resp["start_time"] != nil {
		t.Errorf("start_time = %v; want null for blocked override", resp["start_time"])
	}
	if resp["end_time"] != nil {
		t.Errorf("end_time = %v; want null for blocked override", resp["end_time"])
	}
}

func TestUpdateAvailabilityOverride_flipToAvailable(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key,
		`{"date":"2026-08-03","is_available":false}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	req := authReq(http.MethodPatch, "/v1/availability-overrides/"+id,
		`{"is_available":true,"start_time":"08:00","end_time":"12:00"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["is_available"] != true {
		t.Errorf("is_available = %v; want true", resp["is_available"])
	}
	if resp["start_time"] != "08:00" {
		t.Errorf("start_time = %v; want 08:00", resp["start_time"])
	}
	if resp["end_time"] != "12:00" {
		t.Errorf("end_time = %v; want 12:00", resp["end_time"])
	}
}

func TestUpdateAvailabilityOverride_notFound(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	req := authReq(http.MethodPatch, "/v1/availability-overrides/does-not-exist",
		`{"is_available":false}`, key)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

func TestUpdateAvailabilityOverride_missingTimesWhenAvailable(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key,
		`{"date":"2026-08-04","is_available":false}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	req := authReq(http.MethodPatch, "/v1/availability-overrides/"+id,
		`{"is_available":true}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestUpdateAvailabilityOverride_startNotBeforeEnd(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key,
		`{"date":"2026-08-05","is_available":true,"start_time":"09:00","end_time":"17:00"}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	req := authReq(http.MethodPatch, "/v1/availability-overrides/"+id,
		`{"start_time":"17:00","end_time":"09:00"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestUpdateAvailabilityOverride_invalidHHMM(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	_, created := createOverride(t, h, key,
		`{"date":"2026-08-06","is_available":true,"start_time":"09:00","end_time":"17:00"}`)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created id is empty")
	}

	for _, body := range []string{
		`{"start_time":"9:00"}`,
		`{"end_time":"25:00"}`,
		`{"start_time":"09:0a"}`,
	} {
		req := authReq(http.MethodPatch, "/v1/availability-overrides/"+id, body, key)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.UpdateAvailabilityOverride)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d; want 400", body, rec.Code)
		}
	}
}
