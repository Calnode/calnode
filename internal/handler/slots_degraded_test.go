package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
)

type failingCalendar struct {
	calendar.Provider
}

func (failingCalendar) Name() string { return "google" }
func (failingCalendar) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, errTestProviderDown
}

type testProviderError string

func (e testProviderError) Error() string { return string(e) }

const errTestProviderDown = testProviderError("provider unavailable")

// TestSlots_marksDegradedOnProviderOutage proves a calendar outage surfaces as
// degraded=true on the slots response rather than silently reading as free
// time: surfaces warn, while booking itself stays fail-closed at commit time.
func TestSlots_marksDegradedOnProviderOutage(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	svc := calendar.NewService(database)
	svc.Register(failingCalendar{})
	h.SetCalendar(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots?from=2027-06-18&to=2027-06-18", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["degraded"] != true {
		t.Errorf("slots response has no degraded flag despite provider outage: %v", body["degraded"])
	}
}
