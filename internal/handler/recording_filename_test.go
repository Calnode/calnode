package handler

import "testing"

func TestRecordingFilename(t *testing.T) {
	const iso = "2026-06-28T14:30:00.000Z"
	cases := []struct {
		name, booker, created, want string
	}{
		{"booker + date", "Jane Doe", iso, "Jane-Doe-2026-06-28-1430.mp4"},
		{"no booker falls back", "", iso, "recording-2026-06-28-1430.mp4"},
		{"accents/punctuation stripped to ASCII", "José O'Brien", iso, "Jos-OBrien-2026-06-28-1430.mp4"},
		{"unparseable date drops the stamp", "Jane Doe", "not-a-date", "Jane-Doe.mp4"},
		{"name-only collapses to recording", "!!!", iso, "recording-2026-06-28-1430.mp4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := recordingFilename(c.booker, c.created); got != c.want {
				t.Errorf("recordingFilename(%q, %q) = %q, want %q", c.booker, c.created, got, c.want)
			}
		})
	}
}

func TestConsentWindow(t *testing.T) {
	const start = "2026-06-27T20:41:00.000Z"

	// A finished 12s recording: window is [start-5s, start+12s+60s]. A consent during the meeting
	// is inside it; one from another session in the same room (a day earlier) is excluded.
	lo, hi, ok := consentWindow(start, 12, "complete")
	if !ok {
		t.Fatal("expected ok for a parseable start")
	}
	if lo != "2026-06-27T20:40:55.000Z" {
		t.Errorf("lo = %q, want 2026-06-27T20:40:55.000Z", lo)
	}
	if hi != "2026-06-27T20:42:12.000Z" {
		t.Errorf("hi = %q, want 2026-06-27T20:42:12.000Z", hi)
	}
	in := "2026-06-27T20:41:05.500Z"  // mid-meeting consent
	out := "2026-06-26T18:57:27.000Z" // a different session, prior day
	if !(in >= lo && in <= hi) {
		t.Errorf("mid-meeting consent %q should be within [%q, %q]", in, lo, hi)
	}
	if out >= lo && out <= hi {
		t.Errorf("prior-session consent %q should be outside [%q, %q]", out, lo, hi)
	}

	// Active recording (no duration yet) → wide upper bound that still excludes the prior session.
	_, hiActive, _ := consentWindow(start, 0, "active")
	if hiActive <= hi {
		t.Errorf("active upper bound %q should be wider than the finished one %q", hiActive, hi)
	}

	if _, _, ok := consentWindow("not-a-time", 12, "complete"); ok {
		t.Error("expected ok=false for an unparseable start")
	}
}

func TestSanitizeFilenamePart(t *testing.T) {
	cases := map[string]string{
		"  --Multiple   spaces-- ": "Multiple-spaces",
		"Anna-Maria_Smith":         "Anna-Maria-Smith",
		"":                         "",
		"////":                     "",
	}
	for in, want := range cases {
		if got := sanitizeFilenamePart(in); got != want {
			t.Errorf("sanitizeFilenamePart(%q) = %q, want %q", in, got, want)
		}
	}
}
