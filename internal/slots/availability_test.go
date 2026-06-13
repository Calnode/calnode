package slots

import (
	"testing"
	"time"
)

// mustLoc loads an IANA timezone or fatals the test.
func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("time.LoadLocation(%q): %v", name, err)
	}
	return loc
}

// utcDate builds a UTC midnight time for the given date.
func utcDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ─── parseWallClock ──────────────────────────────────────────────────────────

func TestParseWallClock_UTC(t *testing.T) {
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	got, err := parseWallClock(loc, date, "09:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

func TestParseWallClock_aucklandWinter(t *testing.T) {
	// Auckland standard time (NZST): UTC+12.
	// 2026-06-15 09:00 NZST = 2026-06-14 21:00 UTC.
	// This is the exact worked example from PRD §9.
	loc := mustLoc(t, "Pacific/Auckland")
	date := utcDate(2026, 6, 15)
	got, err := parseWallClock(loc, date, "09:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 6, 14, 21, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Auckland winter 09:00: got %v; want %v", got.UTC(), want)
	}
}

func TestParseWallClock_aucklandSummer(t *testing.T) {
	// Auckland daylight time (NZDT): UTC+13.
	// 2026-12-14 09:00 NZDT = 2026-12-13 20:00 UTC.
	// This is the exact worked example from PRD §9.
	loc := mustLoc(t, "Pacific/Auckland")
	date := utcDate(2026, 12, 14)
	got, err := parseWallClock(loc, date, "09:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 12, 13, 20, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Auckland summer 09:00: got %v; want %v", got.UTC(), want)
	}
}

func TestParseWallClock_newYorkWinter(t *testing.T) {
	// New York EST: UTC-5.
	// 2026-01-15 09:00 EST = 2026-01-15 14:00 UTC.
	loc := mustLoc(t, "America/New_York")
	date := utcDate(2026, 1, 15)
	got, err := parseWallClock(loc, date, "09:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 1, 15, 14, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("NYC winter 09:00: got %v; want %v", got.UTC(), want)
	}
}

func TestParseWallClock_newYorkSummer(t *testing.T) {
	// New York EDT: UTC-4.
	// 2026-07-15 09:00 EDT = 2026-07-15 13:00 UTC.
	loc := mustLoc(t, "America/New_York")
	date := utcDate(2026, 7, 15)
	got, err := parseWallClock(loc, date, "09:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("NYC summer 09:00: got %v; want %v", got.UTC(), want)
	}
}

func TestParseWallClock_invalidFormat(t *testing.T) {
	_, err := parseWallClock(time.UTC, utcDate(2026, 1, 1), "9am")
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestParseWallClock_invalidHour(t *testing.T) {
	_, err := parseWallClock(time.UTC, utcDate(2026, 1, 1), "25:00")
	if err == nil {
		t.Error("expected error for hour > 23")
	}
}

// ─── resolveDay ──────────────────────────────────────────────────────────────

func TestResolveDay_noRulesForDay(t *testing.T) {
	// Host has Monday rules only; date is a Tuesday.
	loc := time.UTC
	// 2026-06-16 is a Tuesday.
	date := utcDate(2026, 6, 16)
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
	}
	windows, err := resolveDay(loc, date, rules, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("expected no windows for Tuesday; got %v", windows)
	}
}

func TestResolveDay_matchingRule(t *testing.T) {
	loc := time.UTC
	// 2026-06-15 is a Monday.
	date := utcDate(2026, 6, 15)
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
	}
	windows, err := resolveDay(loc, date, rules, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window; got %d", len(windows))
	}
	want := Interval{
		Start: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 15, 17, 0, 0, 0, time.UTC),
	}
	if !windows[0].Start.Equal(want.Start) || !windows[0].End.Equal(want.End) {
		t.Errorf("got %v; want %v", windows[0], want)
	}
}

func TestResolveDay_aucklandWeekdayCorrect(t *testing.T) {
	// UTC date 2026-06-15 is Monday in UTC.  resolveDay uses the UTC weekday,
	// and parseWallClock uses the UTC date components in the host's timezone,
	// so "09:00 Monday Auckland" = Jun 14 21:00 UTC.
	loc := mustLoc(t, "Pacific/Auckland")
	date := utcDate(2026, 6, 15)
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
	}
	windows, err := resolveDay(loc, date, rules, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window; got %d: %v", len(windows), windows)
	}
	// Auckland NZST=UTC+12: 09:00 Mon local = 21:00 Sun UTC
	wantStart := time.Date(2026, 6, 14, 21, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 6, 15, 5, 0, 0, 0, time.UTC)
	if !windows[0].Start.Equal(wantStart) {
		t.Errorf("start: got %v; want %v", windows[0].Start, wantStart)
	}
	if !windows[0].End.Equal(wantEnd) {
		t.Errorf("end: got %v; want %v", windows[0].End, wantEnd)
	}
}

func TestResolveDay_overrideBlocksDay(t *testing.T) {
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
	}
	overrides := []AvailabilityOverride{
		{Date: date, IsAvailable: false},
	}
	windows, err := resolveDay(loc, date, rules, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("expected blocked day; got %v", windows)
	}
}

func TestResolveDay_overrideCustomHours(t *testing.T) {
	loc := time.UTC
	date := utcDate(2026, 6, 15)
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
	}
	overrides := []AvailabilityOverride{
		{Date: date, IsAvailable: true, StartTime: "10:00", EndTime: "12:00"},
	}
	windows, err := resolveDay(loc, date, rules, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 window; got %d", len(windows))
	}
	wantStart := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if !windows[0].Start.Equal(wantStart) || !windows[0].End.Equal(wantEnd) {
		t.Errorf("got %v; want %v-%v", windows[0], wantStart, wantEnd)
	}
}

func TestResolveDay_overrideDoesNotMatchOtherDate(t *testing.T) {
	loc := time.UTC
	date := utcDate(2026, 6, 15) // Monday
	rules := []AvailabilityRule{
		{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
	}
	overrides := []AvailabilityOverride{
		// Override on June 16 should not affect June 15
		{Date: utcDate(2026, 6, 16), IsAvailable: false},
	}
	windows, err := resolveDay(loc, date, rules, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(windows) != 1 {
		t.Errorf("expected 1 window from rules; got %d", len(windows))
	}
}

// ─── alignUp ────────────────────────────────────────────────────────────────

func TestAlignUp_alreadyAligned(t *testing.T) {
	// 09:00:00 UTC is exactly on a 30-min boundary (since epoch).
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	got := alignUp(base, 30*time.Minute)
	if !got.Equal(base) {
		t.Errorf("got %v; want %v", got, base)
	}
}

func TestAlignUp_roundsUp(t *testing.T) {
	base := time.Date(2026, 1, 1, 9, 5, 0, 0, time.UTC) // 09:05
	got := alignUp(base, 30*time.Minute)
	want := time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

func TestAlignUp_15minInterval(t *testing.T) {
	base := time.Date(2026, 1, 1, 9, 7, 0, 0, time.UTC)
	got := alignUp(base, 15*time.Minute)
	want := time.Date(2026, 1, 1, 9, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

func TestAlignUp_hourInterval(t *testing.T) {
	base := time.Date(2026, 1, 1, 9, 1, 0, 0, time.UTC)
	got := alignUp(base, 60*time.Minute)
	want := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v; want %v", got, want)
	}
}
