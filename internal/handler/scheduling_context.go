package handler

import (
	"context"

	"github.com/calnode/calnode/internal/slots"
)

// schedulingContext is the server-resolved input to availability calculation.
// Event types resolve live defaults; invitations resolve persisted snapshots.
// The slot engine and host availability loader do not need to know the source.
type schedulingContext struct {
	EventTypeID    string
	Event          slots.EventConfig
	Hosts          []EventHost
	AllowedWindow  *slots.Window
	ShowTakenSlots bool
}

func (h *Handler) eventTypeSchedulingContext(ctx context.Context, slug string) (schedulingContext, error) {
	et, err := h.loadBookableEventType(ctx, slug)
	if err != nil {
		return schedulingContext{}, err
	}
	hosts, err := h.resolveEventTypeHosts(ctx, et.ID)
	if err != nil {
		return schedulingContext{}, err
	}
	return schedulingContext{
		EventTypeID: et.ID,
		Event: slots.EventConfig{
			DurationMinutes: et.DurationMinutes, SlotIntervalMinutes: et.SlotIntervalMinutes,
			BufferBeforeMinutes: et.BufferBeforeMinutes, BufferAfterMinutes: et.BufferAfterMinutes,
			MinNoticeMinutes: et.MinNoticeMinutes, MaxFutureDays: et.MaxFutureDays, RoutingMode: et.RoutingMode,
		},
		Hosts: hosts, ShowTakenSlots: et.ShowTakenSlots,
	}, nil
}
