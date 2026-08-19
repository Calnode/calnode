package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrandingSettings_fallbackLocale_defaultsToEnglish(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	req := authReq(http.MethodGet, "/v1/settings/branding", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetBranding)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get branding: %d — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["fallback_locale"] != "en" {
		t.Errorf("fallback_locale = %v; want \"en\" by default", resp["fallback_locale"])
	}
}

func TestPatchBranding_fallbackLocale_rejectsUnsupportedCode(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	req := authReq(http.MethodPatch, "/v1/settings/branding", `{"fallback_locale":"fr"}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchBranding)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for an unsupported fallback_locale", rec.Code)
	}
}

func TestPatchBranding_fallbackLocale_setsAndPersists(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	preq := authReq(http.MethodPatch, "/v1/settings/branding", `{"fallback_locale":"es"}`, apiKey)
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchBranding)(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", prec.Code, prec.Body.String())
	}

	greq := authReq(http.MethodGet, "/v1/settings/branding", "", apiKey)
	grec := httptest.NewRecorder()
	h.RequireAuth(h.GetBranding)(grec, greq)
	var resp map[string]any
	json.Unmarshal(grec.Body.Bytes(), &resp) //nolint:errcheck
	if resp["fallback_locale"] != "es" {
		t.Errorf("fallback_locale after patch = %v; want \"es\"", resp["fallback_locale"])
	}
}

// TestBookPage_respectsFallbackLocale is the end-to-end path: a visitor whose browser
// asks for a language Calnode doesn't support (French) gets the operator's configured
// fallback (Spanish here), not the hardcoded English default.
func TestBookPage_respectsFallbackLocale(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	preq := authReq(http.MethodPatch, "/v1/settings/branding", `{"fallback_locale":"es"}`, apiKey)
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchBranding)(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("set fallback: %d — %s", prec.Code, prec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/"+slug, nil)
	req.SetPathValue("slug", slug)
	req.Header.Set("Accept-Language", "fr;q=0.9,de;q=0.5") // neither supported
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("book page: %d — %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<html lang="es">`) {
		t.Errorf("book page should resolve to the configured Spanish fallback; body head: %.500s", body)
	}
}
