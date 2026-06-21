package handler

import "testing"

func TestHostsLabel(t *testing.T) {
	mk := func(names ...string) []hostDisplay {
		out := make([]hostDisplay, len(names))
		for i, n := range names {
			out[i] = hostDisplay{Name: n}
		}
		return out
	}
	cases := []struct {
		hosts []hostDisplay
		want  string
	}{
		{mk(), ""},
		{mk("Alex Johnson"), "Alex Johnson"}, // single = full name
		{mk("Alex Johnson", "Sam Lee"), "Alex & Sam"},
		{mk("Alex J", "Sam L", "Jo K"), "Alex, Sam & Jo"},
		{mk("Alex J", "Sam L", "Jo K", "Pat M"), "Alex, Sam, Jo & 1 other"},
		{mk("A B", "C D", "E F", "G H", "I J"), "A, C, E & 2 others"},
	}
	for _, c := range cases {
		if got := hostsLabel(c.hosts); got != c.want {
			t.Errorf("hostsLabel(%d hosts) = %q; want %q", len(c.hosts), got, c.want)
		}
	}
}

func TestProviderMintsPlatform(t *testing.T) {
	cases := []struct {
		locType  string
		provider string
		want     bool
	}{
		{"google_meet", "google", true},     // Meet auto-generates on Google
		{"google_meet", "microsoft", false},  // never fabricate Meet on Microsoft
		{"teams", "microsoft", true},          // Teams auto-generates on Microsoft (work accts)
		{"teams", "google", false},            // never fabricate Teams on Google (the reported bug)
		{"google_meet", "", false},            // no connection → use manual link
		{"teams", "", false},                  // no connection → use manual link
		{"phone", "google", false},            // non-online type never auto-generates
		{"in_person", "microsoft", false},
	}
	for _, tc := range cases {
		if got := providerMintsPlatform(tc.locType, tc.provider); got != tc.want {
			t.Errorf("providerMintsPlatform(%q,%q)=%v; want %v", tc.locType, tc.provider, got, tc.want)
		}
	}
}

func TestValidMeetingLink(t *testing.T) {
	cases := []struct {
		locType, link string
		want          bool
	}{
		{"teams", "https://teams.microsoft.com/l/meetup-join/abc", true},
		{"teams", "https://teams.live.com/meet/123", true},
		{"teams", "https://gov.teams.microsoft.us/l/x", true},
		{"teams", "https://meet.google.com/abc-defg-hij", false}, // wrong platform
		{"teams", "https://example.com/call", false},
		{"teams", "http://teams.microsoft.com/x", false},          // not https
		{"google_meet", "https://meet.google.com/abc-defg-hij", true},
		{"google_meet", "https://teams.microsoft.com/x", false},
		{"google_meet", "not-a-url", false},
		{"link", "https://anything.example.com/x", true},          // non-online: not platform-checked
	}
	for _, tc := range cases {
		if got := validMeetingLink(tc.locType, tc.link); got != tc.want {
			t.Errorf("validMeetingLink(%q,%q)=%v; want %v", tc.locType, tc.link, got, tc.want)
		}
	}
}

func TestValidVideoURL(t *testing.T) {
	cases := []struct {
		locType, v string
		want       bool
	}{
		{"zoom", "https://zoom.us/j/123", true},
		{"zoom", "https://us02web.zoom.us/j/123", true},
		{"zoom", "https://acme.zoomgov.com/j/1", true},
		{"zoom", "https://meet.google.com/x", false}, // wrong host in zoom field
		{"zoom", "https://example.com/room", false},
		{"link", "https://example.com/room", true},   // any https host
		{"link", "http://example.com/room", false},   // not https
		{"custom_video", "https://whereby.com/x", true},
		{"link", "not-a-url", false},
	}
	for _, tc := range cases {
		if got := validVideoURL(tc.locType, tc.v); got != tc.want {
			t.Errorf("validVideoURL(%q,%q)=%v; want %v", tc.locType, tc.v, got, tc.want)
		}
	}
}

func TestValidPhone(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"+1 (415) 555-1234", true},
		{"021 555 1234", true},
		{"555-1234 x123", true},
		{"+64211234567", true},
		{"12345", false},          // too few digits
		{"", false},
		{"call me maybe", false},  // letters
		{"https://x.com", false},
	}
	for _, tc := range cases {
		if got := validPhone(tc.v); got != tc.want {
			t.Errorf("validPhone(%q)=%v; want %v", tc.v, got, tc.want)
		}
	}
}
