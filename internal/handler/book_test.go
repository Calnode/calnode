package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestBookPage_unknownSlug_returns404(t *testing.T) {
	h, _, _ := setupWorkspace(t)

	req := httptest.NewRequest(http.MethodGet, "/book/no-such-event", nil)
	req.SetPathValue("slug", "no-such-event")
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for unknown slug", rec.Code)
	}
}

func TestBookPage_knownSlug_returns200WithHTML(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q; want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Test Meeting") {
		t.Error("response body missing event type name")
	}
	if !strings.Contains(body, slug) {
		t.Error("response body missing slug (used in JS)")
	}
	if !strings.Contains(body, "30 min") {
		t.Error("response body missing duration label")
	}
}

func TestBookPage_inactiveEventType_returns404(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	// Deactivate the event type.
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"is_active":false}`, apiKey)
	patchReq.SetPathValue("slug", slug)
	patchRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: got %d — %s", patchRec.Code, patchRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for inactive event type", rec.Code)
	}
}

func TestBookPage_durationLabels(t *testing.T) {
	cases := []struct {
		mins int
		want string
	}{
		{15, "15 min"},
		{30, "30 min"},
		{45, "45 min"},
		{60, "1 hour"},
		{90, "1 hr 30 min"},
		{120, "2 hours"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(strconv.Itoa(tc.mins)+"min", func(t *testing.T) {
			h, apiKey, _ := setupWorkspace(t)
			slug := fmt.Sprintf("dur-test-%d", tc.mins)
			body := fmt.Sprintf(`{"slug":%q,"name":"Dur Test","duration_minutes":%d}`, slug, tc.mins)
			req := authReq(http.MethodPost, "/v1/event-types", body, apiKey)
			rec := httptest.NewRecorder()
			h.RequireAuth(h.CreateEventType)(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create event type (%d min): %d — %s", tc.mins, rec.Code, rec.Body.String())
			}

			pageReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
			pageReq.SetPathValue("slug", slug)
			pageRec := httptest.NewRecorder()
			h.BookPage(pageRec, pageReq)

			if !strings.Contains(pageRec.Body.String(), tc.want) {
				t.Errorf("want %q in page body", tc.want)
			}
		})
	}
}

func TestBookPage_privateEventType_returns404(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	// Make the event type private.
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"is_public":false}`, apiKey)
	patchReq.SetPathValue("slug", slug)
	patchRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch is_public=false: got %d — %s", patchRec.Code, patchRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for private event type", rec.Code)
	}
}

func TestBookPage_locationLabels(t *testing.T) {
	cases := []struct {
		locType  string
		locValue string
		want     string
	}{
		// Online/video/phone types now require a usable value (no calendar connected
		// in this test), so these fixtures supply a valid one.
		{"zoom", "https://zoom.us/j/123456789", "Zoom"},
		{"google_meet", "https://meet.google.com/abc-defg-hij", "Google Meet"},
		{"teams", "https://teams.microsoft.com/l/meetup-join/x", "Microsoft Teams"},
		{"phone", "+1 555 123 4567", "Phone Call"},
		{"in_person", "123 Main St", "123 Main St"},
		{"in_person", "", "In Person"}, // in-person value stays optional
		{"custom_video", "https://example.com/room", "Video Call"},
		{"link", "https://example.com/room", "Video Call"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.locType+"_"+tc.locValue, func(t *testing.T) {
			h, apiKey, _ := setupWorkspace(t)
			slug := "loc-" + tc.locType
			if tc.locValue != "" {
				slug += "-val"
			}
			body := fmt.Sprintf(
				`{"slug":%q,"name":"Loc Test","duration_minutes":30,"location_type":%q,"location_value":%q}`,
				slug, tc.locType, tc.locValue,
			)
			createReq := authReq(http.MethodPost, "/v1/event-types", body, apiKey)
			createRec := httptest.NewRecorder()
			h.RequireAuth(h.CreateEventType)(createRec, createReq)
			if createRec.Code != http.StatusCreated {
				t.Fatalf("create (%s): %d — %s", tc.locType, createRec.Code, createRec.Body.String())
			}

			pageReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
			pageReq.SetPathValue("slug", slug)
			pageRec := httptest.NewRecorder()
			h.BookPage(pageRec, pageReq)

			if pageRec.Code != http.StatusOK {
				t.Fatalf("BookPage: %d — %s", pageRec.Code, pageRec.Body.String())
			}
			if !strings.Contains(pageRec.Body.String(), tc.want) {
				t.Errorf("location_type=%q value=%q: want %q in page body", tc.locType, tc.locValue, tc.want)
			}
		})
	}
}

func TestBookPage_rendersIntakeQuestions(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	createQuestion(t, h, slug, key, `{"label":"What are your goals?","type":"text","required":true}`)
	createQuestion(t, h, slug, key, `{"label":"Agree to terms","type":"checkbox"}`)

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d — %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "What are your goals?") {
		t.Error("page body missing text question label")
	}
	if !strings.Contains(body, "Agree to terms") {
		t.Error("page body missing checkbox question label")
	}
	if !strings.Contains(body, "required-star") {
		t.Error("page body missing required-star for required field")
	}
}

func TestBookPage_assistantPanelGatedOnLLM(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	render := func() string {
		req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.BookPage(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("BookPage: %d — %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	// AI off (default) → no chat panel.
	if body := render(); strings.Contains(body, "Book by chat") {
		t.Error("assistant panel rendered while LLM is disabled")
	}

	// Enable the LLM (dummy endpoint; reload builds a client without a network call).
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchLLMSettings)(prec, authReq(http.MethodPatch, "/v1/settings/llm",
		`{"enabled":true,"endpoint":"http://example.test/v1","model":"m"}`, apiKey))
	if prec.Code != http.StatusOK {
		t.Fatalf("enable llm: %d — %s", prec.Code, prec.Body.String())
	}

	// AI on → chat panel + assistant endpoint wired.
	body := render()
	if !strings.Contains(body, "Book by chat") || !strings.Contains(body, "/assistant") {
		t.Errorf("assistant panel/script missing when LLM enabled")
	}
}

func TestBookPage_maxFutureDays0(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug := "zero-days"
	body := `{"slug":"zero-days","name":"Zero Days","duration_minutes":30,"max_future_days":0}`
	req := authReq(http.MethodPost, "/v1/event-types", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	pageReq.SetPathValue("slug", slug)
	pageRec := httptest.NewRecorder()
	h.BookPage(pageRec, pageReq)

	if pageRec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d — %s", pageRec.Code, pageRec.Body.String())
	}
	// html/template pads numbers in JS context with spaces, so check via Fields.
	pageBody := pageRec.Body.String()
	maxDaysGot := "(not found)"
	for _, line := range strings.Split(pageBody, "\n") {
		if strings.Contains(line, "MAX_DAYS") {
			fields := strings.Fields(line) // e.g. ["const","MAX_DAYS","=","0",";"]
			if len(fields) >= 4 {
				maxDaysGot = fields[3]
			}
			break
		}
	}
	if maxDaysGot != "0" {
		t.Errorf("want MAX_DAYS = 0 in page body; got MAX_DAYS = %s", maxDaysGot)
	}
}
