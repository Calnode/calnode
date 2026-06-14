package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func createRule(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	CreateAvailabilityRule(http.ResponseWriter, *http.Request)
}, apiKey, body string) (int, map[string]any) {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/availability-rules", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(rec, req)
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

// ---------------------------------------------------------------------------
// UpdateAvailabilityRule
// ---------------------------------------------------------------------------

func TestUpdateAvailabilityRule_updateTimes(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, created := createRule(t, h, key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if code != http.StatusCreated {
		t.Fatalf("create rule: %d — %v", code, created)
	}
	id, _ := created["id"].(string)

	req := authReq(http.MethodPatch, "/v1/availability-rules/"+id,
		`{"start_time":"10:00","end_time":"18:00"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["start_time"] != "10:00" {
		t.Errorf("start_time = %v; want 10:00", resp["start_time"])
	}
	if resp["end_time"] != "18:00" {
		t.Errorf("end_time = %v; want 18:00", resp["end_time"])
	}
}

func TestUpdateAvailabilityRule_updateDayOfWeek(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, created := createRule(t, h, key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if code != http.StatusCreated {
		t.Fatalf("create rule: %d — %v", code, created)
	}
	id, _ := created["id"].(string)

	req := authReq(http.MethodPatch, "/v1/availability-rules/"+id,
		`{"day_of_week":2}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["day_of_week"].(float64) != 2 {
		t.Errorf("day_of_week = %v; want 2", resp["day_of_week"])
	}
}

func TestUpdateAvailabilityRule_notFound(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	req := authReq(http.MethodPatch, "/v1/availability-rules/does-not-exist",
		`{"day_of_week":3}`, key)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

func TestUpdateAvailabilityRule_invalidDayOfWeek(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, created := createRule(t, h, key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if code != http.StatusCreated {
		t.Fatalf("create rule: %d — %v", code, created)
	}
	id, _ := created["id"].(string)

	req := authReq(http.MethodPatch, "/v1/availability-rules/"+id,
		`{"day_of_week":7}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestUpdateAvailabilityRule_invalidHHMM(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, created := createRule(t, h, key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if code != http.StatusCreated {
		t.Fatalf("create rule: %d — %v", code, created)
	}
	id, _ := created["id"].(string)

	req := authReq(http.MethodPatch, "/v1/availability-rules/"+id,
		`{"start_time":"9:00"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestUpdateAvailabilityRule_endNotAfterStart(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	code, created := createRule(t, h, key, `{"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`)
	if code != http.StatusCreated {
		t.Fatalf("create rule: %d — %v", code, created)
	}
	id, _ := created["id"].(string)

	req := authReq(http.MethodPatch, "/v1/availability-rules/"+id,
		`{"start_time":"17:00","end_time":"09:00"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestUpdateAvailabilityRule_conflictWith409(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	// Use a concrete event_type_id so the UNIQUE constraint fires
	// (SQLite treats NULL as distinct, so two global rules never conflict).
	_, etID := seedEventTypeHTTP(t, h, key)

	// Create rule on day 1 scoped to the event type.
	body1 := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`, etID)
	code1, created1 := createRule(t, h, key, body1)
	if code1 != http.StatusCreated {
		t.Fatalf("create rule 1: %d — %v", code1, created1)
	}

	// Create rule on day 2 scoped to the same event type.
	body2 := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":2,"start_time":"09:00","end_time":"17:00"}`, etID)
	code2, created2 := createRule(t, h, key, body2)
	if code2 != http.StatusCreated {
		t.Fatalf("create rule 2: %d — %v", code2, created2)
	}
	id2, _ := created2["id"].(string)

	// Patch rule 2 to have the same day_of_week as rule 1 → conflict.
	req := authReq(http.MethodPatch, "/v1/availability-rules/"+id2,
		`{"day_of_week":1}`, key)
	req.SetPathValue("id", id2)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateAvailabilityRule)(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d; want 409", rec.Code)
	}
}
