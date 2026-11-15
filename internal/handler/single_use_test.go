package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedAvailabilityWindow(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	CreateAvailabilityRule(http.ResponseWriter, *http.Request)
}, apiKey string, weekday int, startTime, endTime string) {
	t.Helper()
	body := fmt.Sprintf(`{"day_of_week":%d,"start_time":%q,"end_time":%q}`, weekday, startTime, endTime)
	req := authReq(http.MethodPost, "/v1/availability-rules", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("seed availability rule: %d — %s", rec.Code, rec.Body.String())
	}
}

func TestEventTypeFrequencyCaps_slotsFilteringAnd409Conflict(t *testing.T) {
	h, database, apiKey, userID := setupWorkspaceWithDB(t)
	h.SetBaseURL("https://cal.formaprime.vc")
	ctx := context.Background()

	// Configure host timezone to America/New_York.
	if _, err := database.ExecContext(ctx, `UPDATE users SET iana_timezone = 'America/New_York' WHERE id = ?`, userID); err != nil {
		t.Fatalf("set user tz: %v", err)
	}

	// Pick a future Monday and Tuesday > 7 days from now.
	targetDay := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 10)
	for targetDay.Weekday() != time.Monday {
		targetDay = targetDay.AddDate(0, 0, 1)
	}
	tuesday := targetDay.AddDate(0, 0, 1)
	nextMonday := targetDay.AddDate(0, 0, 7)

	// Open Monday (1) and Tuesday (2) 09:00-17:00 NY time.
	seedAvailabilityWindow(t, h, apiKey, 1, "09:00", "17:00")
	seedAvailabilityWindow(t, h, apiKey, 2, "09:00", "17:00")

	// Create event type with max_per_day=1, max_per_week=1.
	createBody := `{
		"slug": "founder-intro",
		"name": "Founder Intro",
		"duration_minutes": 60,
		"max_future_days": 0,
		"max_per_day": 1,
		"max_per_week": 1
	}`
	req := authReq(http.MethodPost, "/v1/event-types", createBody, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateEventType: %d — %s", rec.Code, rec.Body.String())
	}
	var etResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &etResp); err != nil {
		t.Fatalf("decode event type: %v", err)
	}
	if etResp["max_per_day"] != float64(1) || etResp["max_per_week"] != float64(1) {
		t.Fatalf("unexpected caps in CreateEventType response: day=%v week=%v", etResp["max_per_day"], etResp["max_per_week"])
	}

	// Book one slot on Monday 14:00 UTC (10:00 EDT / 09:00 EST).
	monSlot := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 14, 0, 0, 0, time.UTC)
	bookBody := fmt.Sprintf(`{
		"event_type_slug": "founder-intro",
		"start_at": %q,
		"name": "Alice Founder",
		"email": "alice@startup.io",
		"timezone": "America/New_York"
	}`, monSlot.Format(time.RFC3339))
	bReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(bookBody))
	bReq.Header.Set("Content-Type", "application/json")
	bRec := httptest.NewRecorder()
	h.CreateBooking(bRec, bReq)
	if bRec.Code != http.StatusCreated {
		t.Fatalf("first booking: %d — %s", bRec.Code, bRec.Body.String())
	}

	// Direct booking on Tuesday in the SAME ISO week must fail with 409 Conflict due to max_per_week=1.
	tueSlot := time.Date(tuesday.Year(), tuesday.Month(), tuesday.Day(), 14, 0, 0, 0, time.UTC)
	tueBody := fmt.Sprintf(`{
		"event_type_slug": "founder-intro",
		"start_at": %q,
		"name": "Bob Founder",
		"email": "bob@startup.io",
		"timezone": "America/New_York"
	}`, tueSlot.Format(time.RFC3339))
	tueReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(tueBody))
	tueReq.Header.Set("Content-Type", "application/json")
	tueRec := httptest.NewRecorder()
	h.CreateBooking(tueRec, tueReq)
	if tueRec.Code != http.StatusConflict {
		t.Fatalf("capped Tuesday booking status = %d; want 409 — %s", tueRec.Code, tueRec.Body.String())
	}

	// GetSlots over Mon..Tue (same ISO week) + next Monday (new ISO week):
	// capped week slots must be excluded while next Monday stays available.
	sReq := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/event-types/founder-intro/slots?from=%s&to=%s&tz=America/New_York",
			targetDay.Format("2006-01-02"), nextMonday.Format("2006-01-02")),
		nil)
	sReq.SetPathValue("slug", "founder-intro")
	sRec := httptest.NewRecorder()
	h.GetSlots(sRec, sReq)
	if sRec.Code != http.StatusOK {
		t.Fatalf("GetSlots: %d — %s", sRec.Code, sRec.Body.String())
	}
	var sResp struct {
		Slots []struct {
			Start string `json:"start"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(sRec.Body.Bytes(), &sResp); err != nil {
		t.Fatalf("decode GetSlots: %v", err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	monStr := targetDay.Format("2006-01-02")
	tueStr := tuesday.Format("2006-01-02")
	nextMonStr := nextMonday.Format("2006-01-02")
	var hasNextMon bool
	for _, sl := range sResp.Slots {
		parsed, err := time.Parse(time.RFC3339, sl.Start)
		if err != nil {
			t.Fatalf("parse slot start: %v", err)
		}
		d := parsed.In(ny).Format("2006-01-02")
		if d == monStr || d == tueStr {
			t.Errorf("unexpected slot on capped week day %s: %s", d, sl.Start)
		}
		if d == nextMonStr {
			hasNextMon = true
		}
	}
	if !hasNextMon {
		t.Errorf("expected slots on uncapped next Monday %s, got %d slots total", nextMonStr, len(sResp.Slots))
	}

	// PATCH max_per_week to null clears it cleanly, restoring Tuesday slots.
	pReq := authReq(http.MethodPatch, "/v1/event-types/founder-intro", `{"max_per_week":null}`, apiKey)
	pReq.SetPathValue("slug", "founder-intro")
	pRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(pRec, pReq)
	if pRec.Code != http.StatusOK {
		t.Fatalf("PatchEventType null clear: %d — %s", pRec.Code, pRec.Body.String())
	}
	var patched map[string]any
	_ = json.Unmarshal(pRec.Body.Bytes(), &patched)
	if patched["max_per_week"] != nil {
		t.Errorf("max_per_week after PATCH null = %v; want nil", patched["max_per_week"])
	}
}

func TestSingleUseLinks_lifecycleBookReplayAndManageReschedule(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	h.SetBaseURL("https://cal.formaprime.vc")
	ctx := context.Background()

	// Seed an event type (even make it private to ensure /t/{token} and single_use_token handle private invites cleanly).
	slug, etID := seedEventTypeHTTP(t, h, apiKey)
	pReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"is_public":false}`, apiKey)
	pReq.SetPathValue("slug", slug)
	pRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(pRec, pReq)
	if pRec.Code != http.StatusOK {
		t.Fatalf("PatchEventType is_public=false: %d", pRec.Code)
	}

	targetDay := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 10)
	for i := 0; i < 7; i++ {
		seedAvailabilityWindow(t, h, apiKey, i, "08:00", "20:00")
	}

	// 1. POST /v1/event-types/{id}/single-use-links
	createReq := authReq(http.MethodPost, "/v1/event-types/"+etID+"/single-use-links", `{
		"expires_in_hours": 168
	}`, apiKey)
	createReq.SetPathValue("id", etID)
	createRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateSingleUseLink)(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateSingleUseLink: %d — %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create link: %v", err)
	}
	if created.Token == "" || created.URL != "https://cal.formaprime.vc/t/"+created.Token {
		t.Fatalf("unexpected link response: %+v", created)
	}

	// 2. GET /v1/event-types/{id}/single-use-links
	listReq := authReq(http.MethodGet, "/v1/event-types/"+etID+"/single-use-links", "", apiKey)
	listReq.SetPathValue("id", etID)
	listRec := httptest.NewRecorder()
	h.RequireAuth(h.ListSingleUseLinks)(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), created.Token) {
		t.Fatalf("ListSingleUseLinks: %d — %s", listRec.Code, listRec.Body.String())
	}

	// 3. GET /t/{token} renders book.html prefilled via URL query params.
	pageReq := httptest.NewRequest(http.MethodGet, "/t/"+created.Token+"?name=Override+Name&email=jane@acme.vc", nil)
	pageReq.SetPathValue("token", created.Token)
	pageRec := httptest.NewRecorder()
	h.SingleUseBookPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("GET /t/{token}: %d — %s", pageRec.Code, pageRec.Body.String())
	}
	htmlBody := pageRec.Body.String()
	for _, want := range []string{created.Token, `value="Override Name"`, `value="jane@acme.vc"`} {
		if !strings.Contains(htmlBody, want) {
			t.Errorf("GET /t/{token} body missing %q", want)
		}
	}

	// 4. Book using single_use_token -> creates booking & atomically burns token.
	slot1 := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 10, 0, 0, 0, time.UTC)
	bookReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": %q,
		"name": "Jane Founder",
		"email": "jane@acme.vc",
		"timezone": "UTC",
		"single_use_token": %q
	}`, slug, slot1.Format(time.RFC3339), created.Token)))
	bookReq.Header.Set("Content-Type", "application/json")
	bookRec := httptest.NewRecorder()
	h.CreateBooking(bookRec, bookReq)
	if bookRec.Code != http.StatusCreated {
		t.Fatalf("CreateBooking with single_use_token: %d — %s", bookRec.Code, bookRec.Body.String())
	}
	var booked struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(bookRec.Body.Bytes(), &booked); err != nil || booked.ID == "" {
		t.Fatalf("decode booking: %v / %s", err, bookRec.Body.String())
	}

	// 5. Replay GET /t/{token} and POST /v1/bookings -> 410 Gone.
	replayPageReq := httptest.NewRequest(http.MethodGet, "/t/"+created.Token, nil)
	replayPageReq.SetPathValue("token", created.Token)
	replayPageRec := httptest.NewRecorder()
	h.SingleUseBookPage(replayPageRec, replayPageReq)
	if replayPageRec.Code != http.StatusGone {
		t.Errorf("replay GET /t/{token} status = %d; want 410", replayPageRec.Code)
	}
	if !strings.Contains(replayPageRec.Body.String(), "This booking link has expired or has already been used") {
		t.Errorf("replay body = %q; want expired/used message", replayPageRec.Body.String())
	}

	slot2 := slot1.Add(2 * time.Hour)
	replayBookReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": %q,
		"name": "Jane Founder",
		"email": "jane@acme.vc",
		"timezone": "UTC",
		"single_use_token": %q
	}`, slug, slot2.Format(time.RFC3339), created.Token)))
	replayBookReq.Header.Set("Content-Type", "application/json")
	replayBookRec := httptest.NewRecorder()
	h.CreateBooking(replayBookRec, replayBookReq)
	if replayBookRec.Code != http.StatusGone {
		t.Errorf("replay CreateBooking status = %d; want 410 — %s", replayBookRec.Code, replayBookRec.Body.String())
	}

	// 6. Created booking's own /manage/{token} and reschedule continue operating without touching single_use_tokens.
	manageToken := issueTestToken(t, database, booked.ID)
	mReq := httptest.NewRequest(http.MethodGet, "/manage/"+manageToken, nil)
	mReq.SetPathValue("token", manageToken)
	mRec := httptest.NewRecorder()
	h.ManagePage(mRec, mReq)
	if mRec.Code != http.StatusOK {
		t.Errorf("GET /manage/{token} status = %d; want 200", mRec.Code)
	}
	newTime := slot1.Add(4 * time.Hour)
	rRec := patchReschedule(t, h, booked.ID, newTime.Format(time.RFC3339), apiKey)
	if rRec.Code != http.StatusOK {
		t.Fatalf("RescheduleBooking status = %d; want 200 — %s", rRec.Code, rRec.Body.String())
	}
	var linkedBookingID string
	if err := database.QueryRowContext(ctx, `SELECT booking_id FROM single_use_tokens WHERE token = ?`, created.Token).
		Scan(&linkedBookingID); err != nil || linkedBookingID != booked.ID {
		t.Errorf("single_use_tokens.booking_id = %q, err=%v; want %q", linkedBookingID, err, booked.ID)
	}

	// 7. Create a second unused token, DELETE /v1/single-use-links/{token} revokes it, then GET /t/{token} returns 410.
	c2Req := authReq(http.MethodPost, "/v1/event-types/"+etID+"/single-use-links", `{"expires_in_hours": 1}`, apiKey)
	c2Req.SetPathValue("id", etID)
	c2Rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateSingleUseLink)(c2Rec, c2Req)
	var c2 struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(c2Rec.Body.Bytes(), &c2)

	delReq := authReq(http.MethodDelete, "/v1/single-use-links/"+c2.Token, "", apiKey)
	delReq.SetPathValue("token", c2.Token)
	delRec := httptest.NewRecorder()
	h.RequireAuth(h.RevokeSingleUseLink)(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/single-use-links/{token} status = %d; want 204 — %s", delRec.Code, delRec.Body.String())
	}

	revokedPageReq := httptest.NewRequest(http.MethodGet, "/t/"+c2.Token, nil)
	revokedPageReq.SetPathValue("token", c2.Token)
	revokedPageRec := httptest.NewRecorder()
	h.SingleUseBookPage(revokedPageRec, revokedPageReq)
	if revokedPageRec.Code != http.StatusGone {
		t.Errorf("revoked GET /t/{token} status = %d; want 410", revokedPageRec.Code)
	}
}
