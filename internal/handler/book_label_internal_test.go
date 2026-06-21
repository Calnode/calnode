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
