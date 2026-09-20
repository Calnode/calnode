package handler

import (
	"context"
	"time"
)

func (h *Handler) calendarFreeHosts(ctx context.Context, et *bookableEventType, candidates, required, optional []string, start, end time.Time) ([]string, []string, error) {
	gc := h.getCal()
	if gc == nil {
		return candidates, optional, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	free := map[string]bool{}
	check := func(id string) (bool, error) {
		if ok, seen := free[id]; seen {
			return ok, nil
		}
		busy, err := gc.FreeBusy(ctx, id, start.Add(-time.Duration(et.BufferBeforeMinutes)*time.Minute), end.Add(time.Duration(et.BufferAfterMinutes)*time.Minute))
		if err != nil {
			return false, err
		}
		for _, iv := range busy {
			if iv.Start.Before(end.Add(time.Duration(et.BufferAfterMinutes)*time.Minute)) && iv.End.After(start.Add(-time.Duration(et.BufferBeforeMinutes)*time.Minute)) {
				free[id] = false
				return false, nil
			}
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
