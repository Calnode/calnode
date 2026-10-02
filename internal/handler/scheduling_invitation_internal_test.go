package handler

import (
	"testing"
	"time"
)

func TestInvitationWindowTimezoneAndDST(t *testing.T) {
	for _, tc := range []struct {
		name, from, until, tz, start, end string
		hours                             time.Duration
	}{
		{"spring DST", "2027-03-14", "2027-03-14", "America/Los_Angeles", "2027-03-14T08:00:00Z", "2027-03-15T07:00:00Z", 23},
		{"fall DST", "2026-11-01", "2026-11-01", "America/Los_Angeles", "2026-11-01T07:00:00Z", "2026-11-02T08:00:00Z", 25},
		{"positive offset", "2026-11-01", "2026-11-02", "Pacific/Auckland", "2026-10-31T11:00:00Z", "2026-11-02T11:00:00Z", 48},
		{"precise offset", "2026-11-01T09:00:00-08:00", "2026-11-01T10:00:00-08:00", "UTC", "2026-11-01T17:00:00Z", "2026-11-01T18:00:00Z", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := invitationWindow(tc.from, tc.until, tc.tz)
			if err != nil {
				t.Fatal(err)
			}
			if start.Format(time.RFC3339) != tc.start || end.Format(time.RFC3339) != tc.end || end.Sub(*start) != tc.hours*time.Hour {
				t.Fatalf("window = %v..%v; want %s..%s", start, end, tc.start, tc.end)
			}
		})
	}
	for _, tc := range []struct{ from, until string }{{"2026-11-02", "2026-11-01"}, {"2026-11-01T10:00:00Z", "2026-11-01T10:00:00Z"}, {"2026-02-30", ""}} {
		if _, _, err := invitationWindow(tc.from, tc.until, "UTC"); err == nil {
			t.Errorf("invalid window accepted: %+v", tc)
		}
	}
	for _, tc := range []struct{ from, until string }{{"2026-11-01", ""}, {"", "2026-11-01"}, {"", ""}} {
		if _, _, err := invitationWindow(tc.from, tc.until, "UTC"); err != nil {
			t.Errorf("optional bounds rejected: %v", err)
		}
	}
}
