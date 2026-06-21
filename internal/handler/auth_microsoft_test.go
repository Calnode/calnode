package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginMicrosoft_returns503WhenNotConfigured(t *testing.T) {
	h, _, _ := authTestSetup(t)
	// microsoftAuth is nil by default — SetMicrosoftAuth not called.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/login", nil)
	rec := httptest.NewRecorder()
	h.LoginMicrosoft(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", rec.Code)
	}
}

func TestLoginMicrosoft_setsStateCookieAndRedirects(t *testing.T) {
	h, _, _ := authTestSetup(t)
	h.SetMicrosoftAuth("client-id", "client-secret", "common", "http://localhost/v1/auth/microsoft/callback", false)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/login", nil)
	rec := httptest.NewRecorder()
	h.LoginMicrosoft(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "login.microsoftonline.com") {
		t.Errorf("Location = %q; want Microsoft auth URL", loc)
	}
	if !strings.Contains(loc, "prompt=select_account") {
		t.Errorf("Location = %q; want prompt=select_account", loc)
	}

	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("calnode_oauth_state cookie not set")
	}
}
