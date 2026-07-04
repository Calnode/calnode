package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// TestSettingsPatch_demoMode_returns503 covers every settings mutation endpoint that
// could produce a real-world side effect (sending email, moving money, consuming a
// stranger's API/video/storage quota) if a demo visitor entered real credentials —
// see the calendar/Zoom connect guards for the same shared-login reasoning. Each of
// these must 503 in demo mode before ever parsing the request body.
func TestSettingsPatch_demoMode_returns503(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		handler func(h *handler.Handler) http.HandlerFunc
	}{
		{"PatchEmailSettings", "/v1/settings/email", func(h *handler.Handler) http.HandlerFunc { return h.PatchEmailSettings }},
		{"TestEmailConnection", "/v1/settings/email/test", func(h *handler.Handler) http.HandlerFunc { return h.TestEmailConnection }},
		{"PatchGoogleSettings", "/v1/settings/google", func(h *handler.Handler) http.HandlerFunc { return h.PatchGoogleSettings }},
		{"PatchZoomSettings", "/v1/settings/zoom", func(h *handler.Handler) http.HandlerFunc { return h.PatchZoomSettings }},
		{"PatchLiveKitSettings", "/v1/settings/livekit", func(h *handler.Handler) http.HandlerFunc { return h.PatchLiveKitSettings }},
		{"PatchLLMSettings", "/v1/settings/llm", func(h *handler.Handler) http.HandlerFunc { return h.PatchLLMSettings }},
		{"TestLLMSettings", "/v1/settings/llm/test", func(h *handler.Handler) http.HandlerFunc { return h.TestLLMSettings }},
		{"PatchNotetakerSettings", "/v1/settings/notetaker", func(h *handler.Handler) http.HandlerFunc { return h.PatchNotetakerSettings }},
		{"PatchStorageSettings", "/v1/settings/storage", func(h *handler.Handler) http.HandlerFunc { return h.PatchStorageSettings }},
		{"PatchStripeSettings", "/v1/settings/stripe", func(h *handler.Handler) http.HandlerFunc { return h.PatchStripeSettings }},
		{"PatchTrackingSettings", "/v1/settings/tracking", func(h *handler.Handler) http.HandlerFunc { return h.PatchTrackingSettings }},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			h, apiKey, _ := setupWorkspace(t)
			h.SetDemoMode(true)

			req := authReq(http.MethodPatch, c.path, `{}`, apiKey)
			rec := httptest.NewRecorder()
			h.RequireAuth(c.handler(h))(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d; want 503 in demo mode", rec.Code)
			}
		})
	}
}
