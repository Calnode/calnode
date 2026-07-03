package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectZoom_demoMode_returns503(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t) // Zoom deliberately left unconfigured
	h.SetDemoMode(true)

	req := authReq(http.MethodGet, "/v1/zoom/connect", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ConnectZoom)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 in demo mode", rec.Code)
	}
}

func TestConnectZoom_notConfigured_returns501(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodGet, "/v1/zoom/connect", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ConnectZoom)(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501 when Zoom not configured", rec.Code)
	}
}
