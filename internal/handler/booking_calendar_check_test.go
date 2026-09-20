package handler_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type conflictCalendar struct {
	calendar.Provider
	busy []slots.Interval
	err  error
}

func (p conflictCalendar) Name() string { return "google" }
func (p conflictCalendar) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return p.busy, p.err
}

func TestBookingRejectsExternalCalendarConflicts(t *testing.T) {
	start := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		provider conflictCalendar
	}{
		{"busy", conflictCalendar{busy: []slots.Interval{{Start: start, End: start.Add(time.Hour)}}}},
		{"unavailable", conflictCalendar{err: errors.New("provider unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, _ := setupWorkspaceWithDB(t)
			slug, _ := seedEventTypeHTTP(t, h, key)
			svc := calendar.NewService(database)
			svc.Register(tc.provider)
			h.SetCalendar(svc)
			req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Test","email":"test@example.com"}`, slug)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.CreateBooking(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var n int
			if err := database.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("created %d bookings", n)
			}
		})
	}
}
