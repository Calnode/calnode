package caldav

import (
	"strings"
	"time"
)

// icsEvent is the minimal slice of a VEVENT we need for conflict checking.
type icsEvent struct {
	start, end  time.Time
	transparent bool // TRANSP:TRANSPARENT — does not block time
	cancelled   bool // STATUS:CANCELLED
}

// parseVEvents extracts the VEVENT components from a VCALENDAR body, pulling DTSTART/DTEND
// (or DURATION) into concrete times. It is deliberately small: it does NOT expand RRULEs —
// FreeBusy asks the server to expand recurrence (CalDAV <C:expand>), so the events handed
// here are already concrete instances. Unparseable events are skipped.
func parseVEvents(data string) []icsEvent {
	lines := unfold(data)
	var out []icsEvent
	var cur *icsEvent
	var startDate bool
	for _, ln := range lines {
		name, params, value := splitLine(ln)
		upper := strings.ToUpper(name)
		switch upper {
		case "BEGIN":
			if strings.EqualFold(value, "VEVENT") {
				cur = &icsEvent{}
				startDate = false
			}
		case "END":
			if strings.EqualFold(value, "VEVENT") && cur != nil {
				// Default end if none was given: all-day → +24h, timed → same as start.
				if cur.end.IsZero() && !cur.start.IsZero() {
					if startDate {
						cur.end = cur.start.Add(24 * time.Hour)
					} else {
						cur.end = cur.start
					}
				}
				out = append(out, *cur)
				cur = nil
			}
		default:
			if cur == nil {
				continue
			}
			switch upper {
			case "DTSTART":
				if t, isDate, ok := parseICSTime(params, value); ok {
					cur.start = t
					startDate = isDate
				}
			case "DTEND":
				if t, _, ok := parseICSTime(params, value); ok {
					cur.end = t
				}
			case "DURATION":
				if d, ok := parseICSDuration(value); ok && !cur.start.IsZero() {
					cur.end = cur.start.Add(d)
				}
			case "TRANSP":
				cur.transparent = strings.EqualFold(strings.TrimSpace(value), "TRANSPARENT")
			case "STATUS":
				cur.cancelled = strings.EqualFold(strings.TrimSpace(value), "CANCELLED")
			}
		}
	}
	return out
}

// unfold joins RFC 5545 folded lines (a continuation line starts with a space or tab) and
// returns the logical lines.
func unfold(data string) []string {
	raw := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	var out []string
	for _, ln := range raw {
		if ln == "" {
			continue
		}
		if (ln[0] == ' ' || ln[0] == '\t') && len(out) > 0 {
			out[len(out)-1] += ln[1:]
			continue
		}
		out = append(out, ln)
	}
	return out
}

// splitLine splits a content line into (name, params, value): "DTSTART;TZID=X:200..." →
// ("DTSTART", {"TZID":"X"}, "200...").
func splitLine(ln string) (name string, params map[string]string, value string) {
	colon := strings.IndexByte(ln, ':')
	if colon < 0 {
		return ln, nil, ""
	}
	head, value := ln[:colon], ln[colon+1:]
	parts := strings.Split(head, ";")
	name = parts[0]
	params = map[string]string{}
	for _, p := range parts[1:] {
		if eq := strings.IndexByte(p, '='); eq >= 0 {
			params[strings.ToUpper(p[:eq])] = strings.Trim(p[eq+1:], `"`)
		}
	}
	return name, params, value
}

// parseICSTime parses an iCalendar date/date-time value, honouring VALUE=DATE and TZID.
// Returns (time, isDateOnly, ok). Floating times (no Z, no TZID) are treated as UTC.
func parseICSTime(params map[string]string, value string) (time.Time, bool, bool) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(params["VALUE"], "DATE") || (len(value) == 8 && !strings.Contains(value, "T")) {
		t, err := time.Parse("20060102", value)
		if err != nil {
			return time.Time{}, false, false
		}
		return t.UTC(), true, true
	}
	if strings.HasSuffix(value, "Z") {
		t, err := time.Parse("20060102T150405Z", value)
		if err != nil {
			return time.Time{}, false, false
		}
		return t.UTC(), false, true
	}
	// Local time, possibly with a TZID.
	loc := time.UTC
	if tz := params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", value, loc)
	if err != nil {
		return time.Time{}, false, false
	}
	return t.UTC(), false, true
}

// parseICSDuration parses an RFC 5545 duration like "PT1H30M", "P1D", "-PT15M".
func parseICSDuration(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	neg := false
	if v[0] == '-' {
		neg = true
		v = v[1:]
	} else if v[0] == '+' {
		v = v[1:]
	}
	if len(v) == 0 || v[0] != 'P' {
		return 0, false
	}
	v = v[1:]
	var total time.Duration
	inTime := false
	num := ""
	for _, r := range v {
		switch {
		case r == 'T':
			inTime = true
		case r >= '0' && r <= '9':
			num += string(r)
		default:
			n := atoi(num)
			num = ""
			switch r {
			case 'W':
				total += time.Duration(n) * 7 * 24 * time.Hour
			case 'D':
				total += time.Duration(n) * 24 * time.Hour
			case 'H':
				total += time.Duration(n) * time.Hour
			case 'M':
				if inTime {
					total += time.Duration(n) * time.Minute
				}
			case 'S':
				total += time.Duration(n) * time.Second
			default:
				return 0, false
			}
		}
	}
	if neg {
		total = -total
	}
	return total, true
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
