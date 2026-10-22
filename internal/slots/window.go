package slots

import (
	"fmt"
	"time"
)

// Window bounds the whole appointment, independently of the requested dates and
// host/booker timezones. Zero Start or End leaves that side unbounded. End is an
// exclusive availability boundary: an appointment may finish exactly at it.
type Window struct {
	Start time.Time
	End   time.Time
}

func (w Window) Validate() error {
	if w.Start.IsZero() && w.End.IsZero() {
		return fmt.Errorf("slots: an allowed window needs at least one bound")
	}
	if !w.Start.IsZero() && !w.End.IsZero() && !w.End.After(w.Start) {
		return fmt.Errorf("slots: allowed window end must follow start")
	}
	return nil
}

// Contains is shared with future booking-time validation so a caller cannot
// offer or commit an appointment whose end crosses the allowed boundary.
func (w Window) Contains(start, end time.Time) bool {
	return end.After(start) && (w.Start.IsZero() || !start.Before(w.Start)) &&
		(w.End.IsZero() || !end.After(w.End))
}
