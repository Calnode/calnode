package handler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

var errCalendarUnavailable = errors.New("calendar availability could not be checked; please try again shortly")

// calendarCheckTimeout bounds the whole booking-time free/busy check. Each host is
// checked concurrently, so this is roughly one provider round-trip (plus a token
// refresh), not one per host — the check used to run host by host under this same
// budget, which a collective with a few slow (often Microsoft Graph) calendars could
// exhaust, failing the booking with errCalendarUnavailable for no fault of any host.
var calendarCheckTimeout = 15 * time.Second

// calendarFreeHosts re-checks external calendar busy at booking time and returns the
// candidates and optional hosts who are actually free.
//
// Fail-closed only where a host's attendance is mandatory: an error on a required host,
// or on any fixed/collective candidate, is errCalendarUnavailable — booking a seat whose
// calendar could not be read risks a double-booking. Elsewhere an unreadable calendar
// just removes that host from consideration: a round-robin candidate is skipped (the
// rotation picks among the hosts verified free), and an optional host is left off the
// booking. One host's revoked token therefore no longer blocks every booking on a team.
func (h *Handler) calendarFreeHosts(ctx context.Context, et *bookableEventType, candidates, required, optional []string, start, end time.Time) ([]string, []string, error) {
	gc := h.getCal()
	if gc == nil {
		return candidates, optional, nil
	}
	ctx, cancel := context.WithTimeout(ctx, calendarCheckTimeout)
	defer cancel()
	window := slots.Interval{
		Start: start.Add(-time.Duration(et.BufferAfterMinutes) * time.Minute),
		End:   end.Add(time.Duration(et.BufferBeforeMinutes) * time.Minute),
	}

	type verdict struct {
		free bool
		err  error
	}
	var ids []string
	for _, group := range [][]string{required, candidates, optional} {
		for _, id := range group {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	verdicts := make(map[string]verdict, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			free, err := h.calendarFreeAt(ctx, gc, id, window)
			mu.Lock()
			verdicts[id] = verdict{free, err}
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	for _, id := range required {
		v := verdicts[id]
		if v.err != nil {
			return nil, nil, v.err
		}
		if !v.free {
			return nil, nil, errSlotUnavailable
		}
	}
	available := make([]string, 0, len(candidates))
	var candidateErr error
	for _, id := range candidates {
		v := verdicts[id]
		switch {
		case v.err != nil && et.RoutingMode != "round_robin":
			return nil, nil, v.err
		case v.err != nil:
			h.logger.WarnContext(ctx, "booking calendar check: skipping rotation host", "host", id, "error", v.err)
			candidateErr = v.err
		case v.free:
			available = append(available, id)
		case et.RoutingMode != "round_robin":
			return nil, nil, errSlotUnavailable
		}
	}
	if len(available) == 0 {
		if candidateErr != nil {
			return nil, nil, candidateErr // nobody verified free, and at least one unknown
		}
		return nil, nil, errSlotUnavailable
	}
	guests := make([]string, 0, len(optional))
	for _, id := range optional {
		v := verdicts[id]
		if v.err != nil {
			h.logger.WarnContext(ctx, "booking calendar check: leaving optional host off", "host", id, "error", v.err)
			continue
		}
		if v.free {
			guests = append(guests, id)
		}
	}
	return available, guests, nil
}

// calendarFreeAt reports whether userID's external calendars are free for the whole
// window, ignoring Calnode's own events (the DB is the source of truth for those).
func (h *Handler) calendarFreeAt(ctx context.Context, gc calendarFreeBusy, userID string, window slots.Interval) (bool, error) {
	ownEvents, err := h.ownCalendarEvents(ctx, userID, window.Start.Format(time.RFC3339Nano), window.End.Format(time.RFC3339Nano))
	if err != nil {
		if ctx.Err() != nil { // the check's deadline, not a DB fault: same "try again" as a slow provider
			return false, fmt.Errorf("%w: load own calendar events: %w", errCalendarUnavailable, err)
		}
		return false, fmt.Errorf("load own calendar events: %w", err)
	}
	busy, err := gc.FreeBusy(ctx, userID, window.Start, window.End)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errCalendarUnavailable, err)
	}
	busy = slots.SubtractIntervals(busy, ownEvents)
	remaining := slots.SubtractIntervals([]slots.Interval{window}, busy)
	return len(remaining) == 1 && remaining[0] == window, nil
}

// calendarFreeBusy is the slice of *calendar.Service the booking-time check uses.
type calendarFreeBusy interface {
	FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error)
}
