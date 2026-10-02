package slots

import (
	"testing"
	"time"
)

func TestAllowedWindow(t *testing.T) {
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	req := Request{
		Event: EventConfig{DurationMinutes: 60, SlotIntervalMinutes: 30},
		Hosts: []HostAvailability{{HostID: "host", Location: time.UTC,
			Rules: []AvailabilityRule{{DayOfWeek: day.Weekday(), StartTime: "09:00", EndTime: "13:00"}},
			Busy:  []Interval{{Start: day.Add(10 * time.Hour), End: day.Add(11 * time.Hour)}}}},
		DateFrom: day, DateTo: day, BookerTZ: time.FixedZone("booker", -7*3600), Now: day.Add(-24 * time.Hour),
	}
	for _, tc := range []struct {
		name        string
		window      *Window
		free, taken int
	}{
		{"unrestricted", nil, 4, 3},
		{"whole appointment", &Window{Start: day.Add(9*time.Hour + 30*time.Minute), End: day.Add(12 * time.Hour)}, 1, 3},
		{"lower bound", &Window{Start: day.Add(11 * time.Hour)}, 3, 0},
		{"upper bound", &Window{End: day.Add(10 * time.Hour)}, 1, 0},
		{"no overlap", &Window{Start: day.Add(15 * time.Hour)}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req.AllowedWindow = tc.window
			result, err := GenerateDetailed(req, Extras{Taken: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Free) != tc.free || len(result.Taken) != tc.taken {
				t.Fatalf("free/taken = %d/%d; want %d/%d", len(result.Free), len(result.Taken), tc.free, tc.taken)
			}
			for _, slot := range append(result.Free, result.Taken...) {
				if tc.window != nil && !tc.window.Contains(slot.Start, slot.End) {
					t.Fatalf("slot outside window: %+v", slot)
				}
			}
		})
	}
	for _, window := range []*Window{{}, {Start: day, End: day}, {Start: day, End: day.Add(-time.Hour)}} {
		req.AllowedWindow = window
		if _, err := Generate(req); err == nil {
			t.Errorf("invalid window %+v accepted", window)
		}
	}
}

func TestAllowedWindowDoesNotReportExcludedNoticeGap(t *testing.T) {
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	req := Request{
		Event: EventConfig{DurationMinutes: 30, SlotIntervalMinutes: 30, MinNoticeMinutes: 180},
		Hosts: []HostAvailability{{HostID: "host", Location: time.UTC,
			Rules: []AvailabilityRule{{DayOfWeek: day.Weekday(), StartTime: "09:00", EndTime: "13:00"}}}},
		DateFrom: day, DateTo: day, BookerTZ: time.UTC, Now: day.Add(9 * time.Hour),
		AllowedWindow: &Window{Start: day.Add(10 * time.Hour), End: day.Add(11 * time.Hour)},
	}
	result, err := GenerateDetailed(req, Extras{NoticeGap: true, Taken: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Free) != 0 || len(result.Taken) != 0 || len(result.NoticeGap) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, slot := range result.NoticeGap {
		if !req.AllowedWindow.Contains(slot.Start, slot.End) {
			t.Fatalf("gap outside window: %+v", slot)
		}
	}
}
