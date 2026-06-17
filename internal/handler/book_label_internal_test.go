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
