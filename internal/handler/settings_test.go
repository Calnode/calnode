package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// patchMe sends PATCH /v1/users/me with the given body and returns the recorder.
func patchMe(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	PatchMe(http.ResponseWriter, *http.Request)
}, body, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/users/me", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchMe)(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// GET /v1/users/me — new preference fields
// ---------------------------------------------------------------------------

func TestGetMe_returnsDefaultPrefs(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	req := authReq(http.MethodGet, "/v1/users/me", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get me: %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)

	if me["time_format"] != "12h" {
		t.Errorf("time_format = %v; want 12h (default)", me["time_format"])
	}
	if me["week_start"] != float64(1) {
		t.Errorf("week_start = %v; want 1 (Monday default)", me["week_start"])
	}
	if me["timezone"] != "UTC" {
		t.Errorf("timezone = %v; want UTC", me["timezone"])
	}
}

// ---------------------------------------------------------------------------
// PATCH /v1/users/me — happy path
// ---------------------------------------------------------------------------

func TestPatchMe_updateTimezone(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	rec := patchMe(t, h, `{"timezone":"Pacific/Auckland"}`, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me["timezone"] != "Pacific/Auckland" {
		t.Errorf("timezone = %v; want Pacific/Auckland", me["timezone"])
	}
}

func TestPatchMe_updateTimeFormat(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	rec := patchMe(t, h, `{"time_format":"24h"}`, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me["time_format"] != "24h" {
		t.Errorf("time_format = %v; want 24h", me["time_format"])
	}
}

func TestPatchMe_updateWeekStart(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	rec := patchMe(t, h, `{"week_start":0}`, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me["week_start"] != float64(0) {
		t.Errorf("week_start = %v; want 0 (Sunday)", me["week_start"])
	}
}

func TestPatchMe_updateAll(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	rec := patchMe(t, h, `{"timezone":"America/New_York","time_format":"24h","week_start":0}`, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me["timezone"] != "America/New_York" {
		t.Errorf("timezone = %v; want America/New_York", me["timezone"])
	}
	if me["time_format"] != "24h" {
		t.Errorf("time_format = %v; want 24h", me["time_format"])
	}
	if me["week_start"] != float64(0) {
		t.Errorf("week_start = %v; want 0", me["week_start"])
	}
}

func TestPatchMe_partialUpdate_othersUnchanged(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	// Set initial state.
	patchMe(t, h, `{"timezone":"Asia/Tokyo","time_format":"24h","week_start":0}`, key)

	// Only update timezone.
	rec := patchMe(t, h, `{"timezone":"Europe/London"}`, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me["timezone"] != "Europe/London" {
		t.Errorf("timezone = %v; want Europe/London", me["timezone"])
	}
	// time_format and week_start must be unchanged.
	if me["time_format"] != "24h" {
		t.Errorf("time_format = %v; want 24h (unchanged)", me["time_format"])
	}
	if me["week_start"] != float64(0) {
		t.Errorf("week_start = %v; want 0 (unchanged)", me["week_start"])
	}
}

func TestPatchMe_persistedAcrossRequests(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	patchMe(t, h, `{"time_format":"24h","week_start":0}`, key)

	// Re-fetch via GET to confirm DB was written.
	req := authReq(http.MethodGet, "/v1/users/me", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get me: %d", rec.Code)
	}
	var me map[string]any
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me["time_format"] != "24h" {
		t.Errorf("time_format = %v; want 24h (persisted)", me["time_format"])
	}
	if me["week_start"] != float64(0) {
		t.Errorf("week_start = %v; want 0 (persisted)", me["week_start"])
	}
}

// ---------------------------------------------------------------------------
// PATCH /v1/users/me — validation errors
// ---------------------------------------------------------------------------

func TestPatchMe_invalidTimezone(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	rec := patchMe(t, h, `{"timezone":"Not/ATimezone"}`, key)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid timezone: got %d; want 400", rec.Code)
	}
}

func TestPatchMe_invalidTimeFormat(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	for _, bad := range []string{`"1h"`, `"24"`, `"noon"`, `""`} {
		rec := patchMe(t, h, `{"time_format":`+bad+`}`, key)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("time_format %s: got %d; want 400", bad, rec.Code)
		}
	}
}

func TestPatchMe_invalidWeekStart(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	for _, bad := range []string{"-1", "7", "10"} {
		rec := patchMe(t, h, `{"week_start":`+bad+`}`, key)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("week_start %s: got %d; want 400", bad, rec.Code)
		}
	}
}

func TestPatchMe_emptyBody_isNoop(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	rec := patchMe(t, h, `{}`, key)
	if rec.Code != http.StatusOK {
		t.Errorf("empty body: got %d; want 200 (no-op)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// PATCH /v1/users/me — auth
// ---------------------------------------------------------------------------

func TestPatchMe_requiresAuth(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	req := httptest.NewRequest(http.MethodPatch, "/v1/users/me", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchMe)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d; want 401", rec.Code)
	}
}
