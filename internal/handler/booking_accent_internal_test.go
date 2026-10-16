package handler

import "testing"

func TestBookingAccentReadableForeground(t *testing.T) {
	for _, tc := range []struct{ color, foreground string }{
		{"#111827", "#ffffff"}, {"#ffe500", "#000000"}, {"#ffffff", "#000000"}, {"#000000", "#ffffff"}, {"invalid", "#ffffff"},
	} {
		if got := accentForeground(tc.color); got != tc.foreground {
			t.Errorf("%s: got %s, want %s", tc.color, got, tc.foreground)
		}
	}
}
