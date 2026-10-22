package handler

import (
	"testing"
)

// mkHosts builds n placeholder faces.
func mkHosts(n int) []hostDisplay {
	out := make([]hostDisplay, n)
	for i := range out {
		name := string(rune('A' + i))
		out[i] = hostDisplay{Name: name, Initial: name, Z: (n - i) * 10}
	}
	return out
}

func TestCapHostFaces(t *testing.T) {
	cases := []struct {
		name         string
		n            int
		wantFaces    int
		wantOverflow int
	}{
		{"none", 0, 0, 0},
		{"single", 1, 1, 0},
		{"at the limit", maxHostFaces, maxHostFaces, 0},
		{"one over", maxHostFaces + 1, maxHostFaces, 1},
		{"six hosts (issue #166)", 6, maxHostFaces, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hosts := mkHosts(tc.n)
			if got := capHostFaces(hosts, maxHostFaces); len(got) != tc.wantFaces {
				t.Errorf("capHostFaces len = %d; want %d", len(got), tc.wantFaces)
			}
			if got := hostsOverflow(hosts, maxHostFaces); got != tc.wantOverflow {
				t.Errorf("hostsOverflow = %d; want %d", got, tc.wantOverflow)
			}
		})
	}
}
