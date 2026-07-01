package handler

import (
	"os"
	"testing"
	"time"
)

// TestMain pins bookingNow for the whole test binary (covers both this package's
// internal tests and the external handler_test package, which share one test binary).
// A long list of existing tests hardcode "future" fixture dates (e.g.
// "2026-06-15T09:00:00Z") that are only future relative to whenever they were
// written — validateBookingTime's real-time min-notice/max-future check would
// otherwise start rejecting them the moment wall-clock time catches up, which is
// exactly the kind of test that silently rots. Pinning "now" to a fixed point safely
// before every fixture date sidesteps that without touching 40+ test bodies.
func TestMain(m *testing.M) {
	bookingNow = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	os.Exit(m.Run())
}
