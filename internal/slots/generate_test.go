package slots_test

import (
	"testing"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func utcDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func utcTime(year int, month time.Month, day, h, m, s int) time.Time {
	return time.Date(year, month, day, h, m, s, 0, time.UTC)
}

func monRules(start, end string) []slots.AvailabilityRule {
	return []slots.AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: start, EndTime: end},
	}
}

func singleHost(id string, loc *time.Location, rules []slots.AvailabilityRule, busy ...slots.Interval) slots.HostAvailability {
	return slots.HostAvailability{
		HostID:   id,
		Location: loc,
		Rules:    rules,
		Busy:     busy,
	}
}

func busyUTC(sh, sm, eh, em int, date time.Time) slots.Interval {
	return slots.Interval{
		Start: time.Date(date.Year(), date.Month(), date.Day(), sh, sm, 0, 0, time.UTC),
		End:   time.Date(date.Year(), date.Month(), date.Day(), eh, em, 0, 0, time.UTC),
	}
}

func startTimes(s []slots.Slot) []time.Time {
	ts := make([]time.Time, len(s))
	for i, sl := range s {
		ts[i] = sl.Start.UTC()
	}
	return ts
}

// ─── basic slot generation ───────────────────────────────────────────────────

func TestGenerate_basicMonday(t *testing.T) {
	// Host: UTC, Mon 09:00-11:00, 30-min duration, 30-min interval.
	// Date: 2026-06-15 (Monday).
	// Expect slots at 09:00, 09:30, 10:00, 10:30.
	loc := time.UTC
	now := utcTime(2026, 6, 14, 0, 0, 0) // Sunday before, no min-notice issues

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "11:00"))},
		DateFrom: utcDate(2026, 6, 15),
		DateTo:   utcDate(2026, 6, 15),
		BookerTZ: loc,
		Now:      now,
	}

	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	wantStarts := []time.Time{
		utcTime(2026, 6, 15, 9, 0, 0),
		utcTime(2026, 6, 15, 9, 30, 0),
		utcTime(2026, 6, 15, 10, 0, 0),
		utcTime(2026, 6, 15, 10, 30, 0),
	}
	assertSlotStarts(t, got, wantStarts)
}

func TestGenerate_noSlotsOnWrongDay(t *testing.T) {
	// Rules only for Monday; date is Tuesday.
	loc := time.UTC
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "17:00"))},
		DateFrom: utcDate(2026, 6, 16), // Tuesday
		DateTo:   utcDate(2026, 6, 16),
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no slots; got %d", len(got))
	}
}

func TestGenerate_durationLargerThanWindow(t *testing.T) {
	// 90-min slot in a 60-min window → no slots.
	loc := time.UTC
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     90,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "10:00"))},
		DateFrom: utcDate(2026, 6, 15),
		DateTo:   utcDate(2026, 6, 15),
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no slots; got %d", len(got))
	}
}

// ─── DST: Auckland (Southern Hemisphere) ─────────────────────────────────────

func TestGenerate_aucklandWinter_slotsLandCorrectUTC(t *testing.T) {
	// PRD §9 worked example:
	// Auckland host, rule "Mon 09:00-10:00".
	// 2026-06-15 is Monday; NZST=UTC+12 → window is [21:00 Sun, 22:00 Sun] UTC.
	// 30-min duration, 30-min interval → one slot: 21:00 UTC (= 09:00 Mon NZST).
	loc := mustLoc(t, "Pacific/Auckland")
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "10:00"))},
		DateFrom: utcDate(2026, 6, 15),
		DateTo:   utcDate(2026, 6, 15),
		BookerTZ: time.UTC,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 09:00-10:00 NZST = 21:00-22:00 UTC the previous day.
	// 60-min window, 30-min slots → two slots: 21:00 and 21:30 UTC.
	wantStarts := []time.Time{
		utcTime(2026, 6, 14, 21, 0, 0),
		utcTime(2026, 6, 14, 21, 30, 0),
	}
	assertSlotStarts(t, got, wantStarts)
}

func TestGenerate_aucklandSummer_slotsLandCorrectUTC(t *testing.T) {
	// PRD §9 worked example:
	// 2026-12-14 Monday; NZDT=UTC+13 → window is [20:00 Sun, 21:00 Sun] UTC.
	loc := mustLoc(t, "Pacific/Auckland")
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       200,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "10:00"))},
		DateFrom: utcDate(2026, 12, 14),
		DateTo:   utcDate(2026, 12, 14),
		BookerTZ: time.UTC,
		// MaxFutureDays=200 from Jun 14 reaches Jan 2027, covering Dec 14.
		Now: utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 09:00 NZDT = 20:00 UTC the previous day. 60-min window → 2 slots.
	wantStarts := []time.Time{
		utcTime(2026, 12, 13, 20, 0, 0),
		utcTime(2026, 12, 13, 20, 30, 0),
	}
	assertSlotStarts(t, got, wantStarts)
}

// ─── DST: New York (Northern Hemisphere) ─────────────────────────────────────

func TestGenerate_newYorkWinterVsSummer(t *testing.T) {
	// Same rule "Mon 09:00-10:00", different UTC result across DST boundary.
	loc := mustLoc(t, "America/New_York")
	rule := monRules("09:00", "10:00")
	now := utcTime(2025, 12, 1, 0, 0, 0) // far enough back

	tests := []struct {
		name      string
		date      time.Time
		wantStart time.Time
	}{
		// Jan 19, 2026: EST=UTC-5 → 09:00 EST = 14:00 UTC (60-min window = 2 slots)
		{"winter EST", utcDate(2026, 1, 19), utcTime(2026, 1, 19, 14, 0, 0)},
		// Jul 6, 2026: EDT=UTC-4 → 09:00 EDT = 13:00 UTC (60-min window = 2 slots)
		{"summer EDT", utcDate(2026, 7, 6), utcTime(2026, 7, 6, 13, 0, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := slots.Request{
				Event: slots.EventConfig{
					DurationMinutes:     30,
					SlotIntervalMinutes: 30,
					RoutingMode:         "fixed",
					MaxFutureDays:       365,
				},
				Hosts:    []slots.HostAvailability{singleHost("h1", loc, rule)},
				DateFrom: tc.date,
				DateTo:   tc.date,
				BookerTZ: time.UTC,
				Now:      now,
			}
			got, err := slots.Generate(req)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(got) == 0 {
				t.Fatalf("expected slots; got none")
			}
			// Assert the first slot starts at the correct UTC time (verifying DST offset).
			if firstUTC := got[0].Start.UTC(); !firstUTC.Equal(tc.wantStart) {
				t.Errorf("first slot: got %v; want %v", firstUTC, tc.wantStart)
			}
		})
	}
}

// ─── buffers ─────────────────────────────────────────────────────────────────

func TestGenerate_bufferBefore(t *testing.T) {
	// Window: 09:00-17:00 UTC. Busy: 10:00-10:30.
	// buffer_before=15min → busy expands to 09:45-10:30.
	// The 09:30 slot (09:30+30=10:00) runs into expanded busy at 09:45.
	// Specifically: slot [09:30, 10:00] overlaps expanded busy [09:45, 10:30].
	// So 09:30 slot is removed. Slots before 09:30 and after 10:30 remain.
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	busy := busyUTC(10, 0, 10, 30, date)

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			BufferBeforeMinutes: 15,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "12:00"), busy)},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	starts := startTimes(got)
	for _, s := range starts {
		// 09:30 slot should NOT appear because its end (10:00) falls within
		// the 15-min buffer zone before the 10:00 busy start.
		if s.Equal(utcTime(2026, 6, 15, 9, 30, 0)) {
			t.Error("09:30 slot should be blocked by buffer_before=15min")
		}
	}
	// 09:00 slot should still be available.
	found0900 := false
	for _, s := range starts {
		if s.Equal(utcTime(2026, 6, 15, 9, 0, 0)) {
			found0900 = true
		}
	}
	if !found0900 {
		t.Error("09:00 slot should be available (clear of buffer)")
	}
}

func TestGenerate_bufferAfter(t *testing.T) {
	// Busy: 10:00-10:30. buffer_after=15min → expanded busy: 10:00-10:45.
	// Slot at 10:30 would start at 10:30, but busy extends to 10:45, so
	// the free interval only starts at 10:45 → next aligned slot is 11:00.
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	busy := busyUTC(10, 0, 10, 30, date)

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			BufferAfterMinutes:  15,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "12:00"), busy)},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	starts := startTimes(got)
	for _, s := range starts {
		if s.Equal(utcTime(2026, 6, 15, 10, 30, 0)) {
			t.Error("10:30 slot should be blocked by buffer_after=15min")
		}
	}
}

// ─── min notice ──────────────────────────────────────────────────────────────

func TestGenerate_minNoticeFiltersEarlySlots(t *testing.T) {
	// Now is 09:00 UTC. min_notice=60min → earliest slot is 10:00.
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	now := utcTime(2026, 6, 15, 9, 0, 0) // same day

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MinNoticeMinutes:    60,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "17:00"))},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: loc,
		Now:      now,
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, s := range got {
		if s.Start.UTC().Before(utcTime(2026, 6, 15, 10, 0, 0)) {
			t.Errorf("slot at %v should be filtered by min_notice=60min", s.Start)
		}
	}
}

// ─── max future ──────────────────────────────────────────────────────────────

func TestGenerate_maxFutureFiltersLateSlots(t *testing.T) {
	// Now is 2026-06-14. max_future_days=1 → only June 15 is reachable.
	// Requesting June 15-16; June 16 slots should be filtered.
	loc := time.UTC
	now := utcTime(2026, 6, 14, 0, 0, 0)

	rule := []slots.AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
		{DayOfWeek: time.Tuesday, StartTime: "09:00", EndTime: "17:00"},
	}

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MaxFutureDays:       1,
			RoutingMode:         "fixed",
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, rule)},
		DateFrom: utcDate(2026, 6, 15),
		DateTo:   utcDate(2026, 6, 16),
		BookerTZ: loc,
		Now:      now,
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cutoff := now.Add(1 * 24 * time.Hour)
	for _, s := range got {
		if s.Start.UTC().After(cutoff) {
			t.Errorf("slot at %v exceeds max_future_days=1 (cutoff %v)", s.Start, cutoff)
		}
	}
}

// ─── date overrides ──────────────────────────────────────────────────────────

func TestGenerate_dateOverrideBlocksDay(t *testing.T) {
	loc := time.UTC
	date := utcDate(2026, 6, 15)

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts: []slots.HostAvailability{{
			HostID:   "h1",
			Location: loc,
			Rules:    monRules("09:00", "17:00"),
			Overrides: []slots.AvailabilityOverride{
				{Date: date, IsAvailable: false},
			},
		}},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 slots (day blocked); got %d", len(got))
	}
}

func TestGenerate_dateOverrideCustomHours(t *testing.T) {
	loc := time.UTC
	date := utcDate(2026, 6, 15)

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts: []slots.HostAvailability{{
			HostID:   "h1",
			Location: loc,
			Rules:    monRules("09:00", "17:00"),
			Overrides: []slots.AvailabilityOverride{
				{Date: date, IsAvailable: true, StartTime: "10:00", EndTime: "11:00"},
			},
		}},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	assertSlotStarts(t, got, []time.Time{
		utcTime(2026, 6, 15, 10, 0, 0),
		utcTime(2026, 6, 15, 10, 30, 0),
	})
}

// ─── routing modes ───────────────────────────────────────────────────────────

func twoHostReq(mode string, h1Rules, h2Rules []slots.AvailabilityRule, h1Busy, h2Busy []slots.Interval) slots.Request {
	return slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         mode,
			MaxFutureDays:       30,
		},
		Hosts: []slots.HostAvailability{
			{HostID: "h1", Location: time.UTC, Rules: h1Rules, Busy: h1Busy},
			{HostID: "h2", Location: time.UTC, Rules: h2Rules, Busy: h2Busy},
		},
		DateFrom: utcDate(2026, 6, 15),
		DateTo:   utcDate(2026, 6, 15),
		BookerTZ: time.UTC,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
}

func TestGenerate_roundRobin_slotOfferedIfAnyFree(t *testing.T) {
	// h1: Mon 09:00-10:00. h2: Mon 10:00-11:00.
	// round_robin → all three half-hours should be offered.
	h1Rules := []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "10:00"}}
	h2Rules := []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "10:00", EndTime: "11:00"}}

	got, err := slots.Generate(twoHostReq("round_robin", h1Rules, h2Rules, nil, nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// h1 covers 09:00 and 09:30; h2 covers 10:00 and 10:30.
	wantStarts := []time.Time{
		utcTime(2026, 6, 15, 9, 0, 0),
		utcTime(2026, 6, 15, 9, 30, 0),
		utcTime(2026, 6, 15, 10, 0, 0),
		utcTime(2026, 6, 15, 10, 30, 0),
	}
	assertSlotStarts(t, got, wantStarts)
}

func TestGenerate_collective_slotOfferedOnlyIfAllFree(t *testing.T) {
	// h1: Mon 09:00-11:00. h2: Mon 10:00-12:00.
	// Overlap: 10:00-11:00. Only those slots offered.
	h1Rules := []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "11:00"}}
	h2Rules := []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "10:00", EndTime: "12:00"}}

	got, err := slots.Generate(twoHostReq("collective", h1Rules, h2Rules, nil, nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wantStarts := []time.Time{
		utcTime(2026, 6, 15, 10, 0, 0),
		utcTime(2026, 6, 15, 10, 30, 0),
	}
	assertSlotStarts(t, got, wantStarts)
}

func TestGenerate_collective_noOverlap_noSlots(t *testing.T) {
	h1Rules := []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "10:00"}}
	h2Rules := []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "11:00", EndTime: "12:00"}}

	got, err := slots.Generate(twoHostReq("collective", h1Rules, h2Rules, nil, nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("collective with no overlap: expected 0 slots; got %d", len(got))
	}
}

func TestGenerate_priority_firstHostPreferred(t *testing.T) {
	// Both hosts free for the same window.
	// priority mode: first host (h1) should be assigned.
	rules := monRules("09:00", "10:00")
	got, err := slots.Generate(twoHostReq("priority", rules, rules, nil, nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, s := range got {
		if s.HostID != "h1" {
			t.Errorf("expected h1 (first in priority list); got %q", s.HostID)
		}
	}
}

func TestGenerate_priority_fallsBackToSecond(t *testing.T) {
	// h1 is fully busy; h2 is free.
	date := utcDate(2026, 6, 15)
	h1Busy := []slots.Interval{busyUTC(9, 0, 17, 0, date)}
	rules := monRules("09:00", "10:00")

	got, err := slots.Generate(twoHostReq("priority", rules, rules, h1Busy, nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected slots from h2; got none")
	}
	for _, s := range got {
		if s.HostID != "h2" {
			t.Errorf("expected h2 (fallback); got %q", s.HostID)
		}
	}
}

// ─── busy time ───────────────────────────────────────────────────────────────

func TestGenerate_busyBlocksSlot(t *testing.T) {
	// Window 09:00-11:00; busy 09:30-10:00 → removes the 09:30 slot.
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	busy := busyUTC(9, 30, 10, 0, date)

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "11:00"), busy)},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, s := range got {
		if s.Start.UTC().Equal(utcTime(2026, 6, 15, 9, 30, 0)) {
			t.Error("09:30 slot should be removed by busy interval")
		}
	}
}

// ─── booker timezone rendering ───────────────────────────────────────────────

func TestGenerate_slotRenderedInBookerTZ(t *testing.T) {
	// Host UTC, Mon 09:00-09:30. Booker in America/New_York (EDT=UTC-4).
	// Slot starts at 09:00 UTC → 05:00 EDT.
	loc := time.UTC
	bookerTZ := mustLoc(t, "America/New_York")
	date := utcDate(2026, 7, 6) // Monday, EDT

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, monRules("09:00", "09:30"))},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: bookerTZ,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 slot; got %d", len(got))
	}
	// Slot should be in New York timezone.
	s := got[0]
	wantHour := 5 // 09:00 UTC = 05:00 EDT
	if s.Start.Hour() != wantHour {
		t.Errorf("slot local hour = %d; want %d (EDT=UTC-4)", s.Start.Hour(), wantHour)
	}
	if s.Start.Location() != bookerTZ {
		t.Errorf("slot location = %v; want %v", s.Start.Location(), bookerTZ)
	}
}

// ─── multi-day range ─────────────────────────────────────────────────────────

func TestGenerate_multiDay(t *testing.T) {
	// Mon + Tue rules; request Mon-Tue → slots on both days.
	loc := time.UTC
	rules := []slots.AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "09:30"},
		{DayOfWeek: time.Tuesday, StartTime: "09:00", EndTime: "09:30"},
	}
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", loc, rules)},
		DateFrom: utcDate(2026, 6, 15), // Monday
		DateTo:   utcDate(2026, 6, 16), // Tuesday
		BookerTZ: loc,
		Now:      utcTime(2026, 6, 14, 0, 0, 0),
	}
	got, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	assertSlotStarts(t, got, []time.Time{
		utcTime(2026, 6, 15, 9, 0, 0),
		utcTime(2026, 6, 16, 9, 0, 0),
	})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func assertSlotStarts(t *testing.T, got []slots.Slot, wantUTC []time.Time) {
	t.Helper()
	if len(got) != len(wantUTC) {
		gotStarts := startTimes(got)
		t.Fatalf("len(got)=%d len(want)=%d\ngot:  %v\nwant: %v", len(got), len(wantUTC), gotStarts, wantUTC)
	}
	for i, want := range wantUTC {
		gotUTC := got[i].Start.UTC()
		if !gotUTC.Equal(want) {
			t.Errorf("[%d] start: got %v; want %v", i, gotUTC, want)
		}
	}
}
