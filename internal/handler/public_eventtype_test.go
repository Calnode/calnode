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

func TestPublicEventType_assistantGreeting(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	fetchGreeting := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/public", nil)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.PublicEventType(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("public: %d — %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		greeting, _ := resp["assistant_greeting"].(string)
		return greeting
	}

	// No override: falls back to the locale-keyed default (English by default).
	if got := fetchGreeting(); got == "" || got != "Hi! Tell me roughly when you'd like to meet — e.g. \"Tuesday afternoon\" or \"next week\" — and I'll find a time. You can always use the calendar instead." {
		t.Errorf("default greeting = %q; want the locale-keyed default", got)
	}

	// Set a custom override via PATCH.
	custom := "Hey there! Let's find a time for our chat."
	preq := authReq(http.MethodPatch, "/v1/event-types/"+slug,
		`{"msg_greeting":"`+custom+`"}`, apiKey)
	preq.SetPathValue("slug", slug)
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", prec.Code, prec.Body.String())
	}
	var patched map[string]any
	json.Unmarshal(prec.Body.Bytes(), &patched) //nolint:errcheck
	if patched["msg_greeting"] != custom {
		t.Errorf("patched msg_greeting = %v; want %q", patched["msg_greeting"], custom)
	}

	if got := fetchGreeting(); got != custom {
		t.Errorf("greeting after override = %q; want %q", got, custom)
	}

	// Clearing it (empty string) reverts to the default, not an empty greeting.
	clearReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"msg_greeting":""}`, apiKey)
	clearReq.SetPathValue("slug", slug)
	clearRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear patch: %d — %s", clearRec.Code, clearRec.Body.String())
	}
	if got := fetchGreeting(); got == "" || got == custom {
		t.Errorf("greeting after clearing override = %q; want the default back", got)
	}
}

// TestPublicEventType_varyHeader is a regression test: the response body varies by
// Accept-Language/Cookie (locale, i18n string table, location_label, assistant_greeting),
// and this endpoint is CORS-wrapped for the embed widget — without Vary, a reverse
// proxy/CDN with a forced-TTL cache rule (Cloudflare "Cache Everything", etc.) could serve
// one visitor's language to every subsequent visitor, even though there's no explicit
// Cache-Control on this endpoint. See internal-docs/i18n-plan.md's caching finding.
func TestPublicEventType_varyHeader(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/public", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.PublicEventType(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public: %d — %s", rec.Code, rec.Body.String())
	}
	vary := rec.Header().Values("Vary")
	joined := strings.Join(vary, ", ")
	if !strings.Contains(joined, "Accept-Language") {
		t.Errorf("Vary header %v missing Accept-Language", vary)
	}
	if !strings.Contains(joined, "Cookie") {
		t.Errorf("Vary header %v missing Cookie (the calnode_lang override)", vary)
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
