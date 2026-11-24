package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// html/template pads a bool in script context with spaces ("SHOW_NEXT =  true ;"), so match
// the shape rather than the exact bytes.
var showNextTrue = regexp.MustCompile(`SHOW_NEXT\s*=\s*true\b`)
var showNextFalse = regexp.MustCompile(`SHOW_NEXT\s*=\s*false\b`)

// The next-available line is opt-in per event type (#254): off unless asked for, settable on
// create and on PATCH, and rendered on the booking page only when on.

func TestShowNextAvailable_defaultsOffAndRoundTripsThroughPatch(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	get := func() map[string]any {
		req := authReq(http.MethodGet, "/v1/event-types/"+slug, "", key)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.GetEventType)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get: %d — %s", rec.Code, rec.Body.String())
		}
		var et map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &et); err != nil {
			t.Fatal(err)
		}
		return et
	}
	if got := get()["show_next_available"]; got != false {
		t.Fatalf("show_next_available = %v on a new event type; want false", got)
	}

	patch := func(body string) {
		req := authReq(http.MethodPatch, "/v1/event-types/"+slug, body, key)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PatchEventType)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %s: %d — %s", body, rec.Code, rec.Body.String())
		}
	}
	patch(`{"show_next_available":true}`)
	if got := get()["show_next_available"]; got != true {
		t.Errorf("show_next_available = %v after PATCH true; want true", got)
	}
	// An unrelated PATCH must leave it alone: a nil pointer means "not mentioned".
	patch(`{"name":"Renamed"}`)
	if got := get()["show_next_available"]; got != true {
		t.Errorf("show_next_available = %v after an unrelated PATCH; want it left true", got)
	}
	patch(`{"show_next_available":false}`)
	if got := get()["show_next_available"]; got != false {
		t.Errorf("show_next_available = %v after PATCH false; want false", got)
	}
}

func TestBookPage_nextAvailableLinkOnlyWhenEnabled(t *testing.T) {
	h, key, _ := setupWorkspace(t)

	render := func(slug, body string) string {
		req := authReq(http.MethodPost, "/v1/event-types", body, key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d — %s", slug, rec.Code, rec.Body.String())
		}
		pageReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
		pageReq.SetPathValue("slug", slug)
		pageRec := httptest.NewRecorder()
		h.BookPage(pageRec, pageReq)
		if pageRec.Code != http.StatusOK {
			t.Fatalf("BookPage %s: %d", slug, pageRec.Code)
		}
		return pageRec.Body.String()
	}

	on := render("next-on", `{"slug":"next-on","name":"On","duration_minutes":30,"show_next_available":true}`)
	if !strings.Contains(on, `class="next-available`) {
		t.Error("enabled event type: page has no next-available button")
	}
	if !showNextTrue.MatchString(on) {
		t.Error("enabled event type: SHOW_NEXT is not true in the page script")
	}

	off := render("next-off", `{"slug":"next-off","name":"Off","duration_minutes":30}`)
	if strings.Contains(off, `class="next-available`) {
		t.Error("default event type: page renders a next-available button")
	}
	if !showNextFalse.MatchString(off) {
		t.Error("default event type: SHOW_NEXT is not false in the page script")
	}
}

// The reschedule page carries the same calendar, so it gets the same opt-in (#254).
func TestManagePage_nextAvailableLinkOnlyWhenEnabled(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	tok := issueTestToken(t, database, createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z"))

	render := func() string {
		req := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
		req.SetPathValue("token", tok)
		rec := httptest.NewRecorder()
		h.ManagePage(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ManagePage: %d", rec.Code)
		}
		return rec.Body.String()
	}

	off := render()
	if strings.Contains(off, `class="next-available`) || !showNextFalse.MatchString(off) {
		t.Error("default event type: reschedule page shows or enables next-available")
	}

	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"show_next_available":true}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	on := render()
	if !strings.Contains(on, `class="next-available`) || !showNextTrue.MatchString(on) {
		t.Error("enabled event type: reschedule page has no next-available button")
	}
}
