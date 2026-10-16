package handler

import "github.com/calnode/calnode/internal/calendar"

// setGoogleCalendar replaces only Google's provider. Publish a fresh service so
// in-flight operations can finish on the previous snapshot without map races.
func (h *Handler) setGoogleCalendar(google calendar.Provider) {
	h.calMu.Lock()
	defer h.calMu.Unlock()
	next := calendar.NewService(h.db)
	if h.cal != nil {
		if primary := h.cal.Primary(); primary != nil && primary.Name() != "google" {
			next.Register(primary)
		} else if google != nil {
			next.Register(google)
		}
		for _, name := range h.cal.ProviderNames() {
			if name != "google" && next.Provider(name) == nil {
				next.Register(h.cal.Provider(name))
			}
		}
	}
	if google != nil && next.Provider("google") == nil {
		next.Register(google)
	}
	if next.Any() {
		h.cal = next
	} else {
		h.cal = nil
	}
}
