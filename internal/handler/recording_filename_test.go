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
