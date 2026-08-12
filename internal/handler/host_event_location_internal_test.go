package handler

import "testing"

// A LiveKit booking's host-calendar event invites the attendee as a guest (Google/Microsoft/
// CalDAV then email that invite's Location straight to them) — so it must never carry the
// privileged host-role join link (meetURL, for LiveKit), only the plain attendee-safe one.
// Regression test for a live-observed bug: an attendee opening the invite the calendar provider
// sent them landed on the host's own link and was immediately granted host controls.
func TestHostEventLocation_liveKitUsesAttendeeSafeLink(t *testing.T) {
	got := hostEventLocation(
		"https://book.example.com/room/booking-1?t=hosttoken",     // meetURL: host-role link
		"https://book.example.com/room/booking-1?t=hosttoken",     // livekitHostURL: non-empty → LiveKit booking
		"https://book.example.com/room/booking-1?t=attendeetoken", // the attendee-safe link
	)
	want := "https://book.example.com/room/booking-1?t=attendeetoken"
	if got != want {
		t.Errorf("hostEventLocation() = %q; want the attendee-safe link %q, not the host-role meetURL", got, want)
	}
}

// Non-LiveKit bookings (Zoom, a manual link, or nothing yet) are unaffected: meetURL is already
// attendee-safe (or empty until an auto-generated Meet link fills it in later), so it passes
// through unchanged.
func TestHostEventLocation_nonLiveKitPassesThroughMeetURL(t *testing.T) {
	cases := []struct {
		name    string
		meetURL string
	}{
		{"empty until primary's event auto-generates one", ""},
		{"a manual/Zoom link", "https://zoom.us/j/123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hostEventLocation(tc.meetURL, "" /* not a LiveKit booking */, "https://book.example.com/manage/xyz")
			if got != tc.meetURL {
				t.Errorf("hostEventLocation() = %q; want meetURL %q unchanged", got, tc.meetURL)
			}
		})
	}
}
