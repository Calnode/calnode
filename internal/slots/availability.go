package slots

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AvailabilityRule is a recurring weekly rule in the host's local wall-clock time.
// DayOfWeek follows Go's time.Weekday: 0=Sunday … 6=Saturday.
type AvailabilityRule struct {
	DayOfWeek time.Weekday
	StartTime string // "HH:MM"
	EndTime   string // "HH:MM"
}

// AvailabilityOverride replaces weekly rules for a specific local date.
// When IsAvailable is false the entire day is blocked.
// When true, StartTime/EndTime define custom hours for that day.
type AvailabilityOverride struct {
	// Date is the host-local calendar date this override applies to.
	// Only Year/Month/Day are significant; time component is ignored.
	Date        time.Time
	IsAvailable bool
	StartTime   string // "HH:MM"; only used when IsAvailable
	EndTime     string // "HH:MM"; only used when IsAvailable
}

// ResolveDayWindows is resolveDay exported for callers outside this package that need
// to check a single candidate interval against a host's configured hours without
// running the full Generate pipeline (e.g. validating a booking-creation request's
// start_at actually falls within the host's availability, independent of the
// external-calendar free/busy fetch Generate also does).
func ResolveDayWindows(loc *time.Location, date time.Time, rules []AvailabilityRule, overrides []AvailabilityOverride) ([]Interval, error) {
	return resolveDay(loc, date, rules, overrides)
}

// resolveDay returns the UTC availability windows for a single host on a single
// calendar date, handling date-specific overrides and resolving wall-clock rules
// to UTC per-date so DST transitions are correct (§6.3).
//
// date should be a UTC midnight representing the calendar date to evaluate.
func resolveDay(loc *time.Location, date time.Time, rules []AvailabilityRule, overrides []AvailabilityOverride) ([]Interval, error) {
	// A date override takes priority over weekly rules.
	for _, ov := range overrides {
		if sameLocalDate(ov.Date, date, loc) {
			if !ov.IsAvailable {
				return nil, nil // full day blocked
			}
			iv, err := wallClockInterval(loc, date, ov.StartTime, ov.EndTime)
			if err != nil {
				return nil, fmt.Errorf("override on %s: %w", date.Format("2006-01-02"), err)
			}
			if !iv.IsEmpty() {
				return []Interval{iv}, nil
			}
			return nil, nil
		}
	}

	// No override: apply all matching weekly rules.
	// Use the UTC weekday of the iteration date, which matches the UTC date
	// components passed to parseWallClock.  Both must agree: the weekday pick
	// and the date.Year/Month/Day used to build the wall-clock time must
	// refer to the same calendar date.
	localWeekday := date.Weekday()

	var windows []Interval
	for _, r := range rules {
		if r.DayOfWeek != localWeekday {
			continue
		}
		iv, err := wallClockInterval(loc, date, r.StartTime, r.EndTime)
		if err != nil {
			return nil, fmt.Errorf("rule %v %s-%s: %w", r.DayOfWeek, r.StartTime, r.EndTime, err)
		}
		if !iv.IsEmpty() {
			windows = append(windows, iv)
		}
	}
	return windows, nil
}

// wallClockInterval converts two "HH:MM" strings on a specific date in loc to
// a UTC Interval.  This is the per-date DST resolution step (§6.3): the same
// wall-clock time maps to different UTC instants across a DST boundary.
func wallClockInterval(loc *time.Location, date time.Time, startHHMM, endHHMM string) (Interval, error) {
	start, err := parseWallClock(loc, date, startHHMM)
	if err != nil {
		return Interval{}, fmt.Errorf("start: %w", err)
	}
	end, err := parseWallClock(loc, date, endHHMM)
	if err != nil {
		return Interval{}, fmt.Errorf("end: %w", err)
	}
	return Interval{Start: start, End: end}, nil
}

// parseWallClock converts "HH:MM" on a given calendar date in loc to a UTC
// time.Time.  Go's time.Date handles the DST transition automatically — it
// creates the correct UTC instant for that wall-clock moment.
func parseWallClock(loc *time.Location, date time.Time, hhmm string) (time.Time, error) {
	parts := strings.SplitN(hhmm, ":", 2)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid time %q: want HH:MM", hhmm)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("invalid hour in %q", hhmm)
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil || min < 0 || min > 59 {
		return time.Time{}, fmt.Errorf("invalid minute in %q", hhmm)
	}
	// Use the UTC year/month/day of the iteration date, but interpret the
	// hour:min in the host's timezone.  This is exactly what §6.3 requires:
	// per-date UTC resolution.
	t := time.Date(date.Year(), date.Month(), date.Day(), hour, min, 0, 0, loc)
	return t.UTC(), nil
}

// sameLocalDate reports whether two times fall on the same calendar date
// when viewed in loc.  Used to match availability overrides.
func sameLocalDate(a, b time.Time, loc *time.Location) bool {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	return ay == by && am == bm && ad == bd
}
