package handler

import "time"

// SetCalendarCheckTimeout overrides the booking-time free/busy deadline for a test and
// returns the restore func.
func SetCalendarCheckTimeout(d time.Duration) (restore func()) {
	orig := calendarCheckTimeout
	calendarCheckTimeout = d
	return func() { calendarCheckTimeout = orig }
}
