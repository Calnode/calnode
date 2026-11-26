package handler

import (
	"testing"
	"time"
)

func TestReminderRunAt(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		startAt     time.Time
		hoursBefore int
		wantOK      bool
		wantRunAt   time.Time
	}{
		{"future reminder keeps its time", now.Add(48 * time.Hour), 24, true, now.Add(24 * time.Hour)},
		{"booking inside the window skips it", now.Add(5 * time.Hour), 24, false, time.Time{}},
		{"shorter reminder in the same booking still runs", now.Add(5 * time.Hour), 2, true, now.Add(3 * time.Hour)},
		{"just late, inside grace, runs now", now.Add(2*time.Hour - 2*time.Minute), 2, true, now},
		{"exactly at the grace edge runs now", now.Add(2*time.Hour - reminderGrace), 2, true, now},
		{"past the grace window is skipped", now.Add(2*time.Hour - reminderGrace - time.Second), 2, false, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := reminderRunAt(tc.startAt, tc.hoursBefore, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if ok && !got.Equal(tc.wantRunAt) {
				t.Errorf("run_at = %s; want %s", got, tc.wantRunAt)
			}
		})
	}
}
