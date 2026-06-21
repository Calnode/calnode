package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicEventType_returnsPublicInfo(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	body := `{"slug":"intro","name":"Intro Call","duration_minutes":30,` +
		`"location_type":"phone","location_value":"+1 555 123 4567","description":"Quick chat"}`
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d — %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/intro/public", nil)
	req.SetPathValue("slug", "intro")
	prec := httptest.NewRecorder()
	h.PublicEventType(prec, req)
	if prec.Code != http.StatusOK {
		t.Fatalf("public: %d — %s", prec.Code, prec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(prec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["name"] != "Intro Call" {
		t.Errorf("name = %v; want Intro Call", resp["name"])
	}
	if resp["location_label"] != "Phone Call" {
		t.Errorf("location_label = %v; want Phone Call", resp["location_label"])
	}
	if d, _ := resp["duration_minutes"].(float64); d != 30 {
		t.Errorf("duration_minutes = %v; want 30", resp["duration_minutes"])
	}
	// Must not leak the raw location value (phone number) — only the label.
	if strings.Contains(prec.Body.String(), "555 123 4567") {
		t.Errorf("public payload leaked the raw location value: %s", prec.Body.String())
	}
}

func TestPublicEventType_404ForUnknown(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/nope/public", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()
	h.PublicEventType(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}
