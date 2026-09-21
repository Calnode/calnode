package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

var errCalendarUnavailable = errors.New("calendar availability could not be checked; please try again shortly")

func (h *Handler) calendarFreeHosts(ctx context.Context, et *bookableEventType, candidates, required, optional []string, start, end time.Time) ([]string, []string, error) {
	gc := h.getCal()
	if gc == nil {
		return candidates, optional, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	window := slots.Interval{
		Start: start.Add(-time.Duration(et.BufferAfterMinutes) * time.Minute),
		End:   end.Add(time.Duration(et.BufferBeforeMinutes) * time.Minute),
	}
	free := map[string]bool{}
	check := func(id string) (bool, error) {
		if ok, seen := free[id]; seen {
			return ok, nil
		}
		ownEvents, err := h.ownCalendarEvents(ctx, id, window.Start.Format(time.RFC3339Nano), window.End.Format(time.RFC3339Nano))
		if err != nil {
			return false, fmt.Errorf("load own calendar events: %w", err)
		}
		busy, err := gc.FreeBusy(ctx, id, window.Start, window.End)
		if err != nil {
			return false, fmt.Errorf("%w: %w", errCalendarUnavailable, err)
		}
		busy = slots.SubtractIntervals(busy, ownEvents)
		remaining := slots.SubtractIntervals([]slots.Interval{window}, busy)
		if len(remaining) != 1 || remaining[0] != window {
			free[id] = false
			return false, nil
		}
		free[id] = true
		return true, nil
	}
	for _, id := range required {
		ok, err := check(id)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, errSlotUnavailable
		}
	}
	available := make([]string, 0, len(candidates))
	for _, id := range candidates {
		ok, err := check(id)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			available = append(available, id)
		} else if et.RoutingMode != "round_robin" {
			return nil, nil, errSlotUnavailable
		}
	}
	if len(available) == 0 {
		return nil, nil, errSlotUnavailable
	}
	guests := make([]string, 0, len(optional))
	for _, id := range optional {
		ok, err := check(id)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			guests = append(guests, id)
		}
	}
	return available, guests, nil
}
