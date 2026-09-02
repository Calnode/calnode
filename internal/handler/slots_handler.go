package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

type slotJSON struct {
	Start   string   `json:"start"`
	End     string   `json:"end"`
	HostIDs []string `json:"host_ids"`
}

// slotsResult is what computeSlots produces: the bookable slots, the host display map,
// and - only for surfaces that asked and event types that opted in - the starts a
// booking took away.
type slotsResult struct {
	Slots []slotJSON
	Taken []slotJSON
	Hosts map[string]map[string]string
	// ShowsTaken records whether Taken was computed at all, which is not the same
	// question as whether it is empty: an opted-in event type with a clear day has no
	// taken slots and must still render as one that greys them. Relying on the slice
	// being nil does not work, because converting an empty result yields an empty
	// non-nil slice.
	ShowsTaken bool
}

// Sentinel errors from computeSlots, so non-HTTP callers (the MCP tools) can map
// failures to their own protocol rather than to HTTP status codes.
var (
	errEventTypeNotFound = errors.New("event type not found")
	errInvalidTimezone   = errors.New("invalid timezone")
	errBadDateRange      = errors.New("from/to must be YYYY-MM-DD and from <= to")
)

// GetSlots handles GET /v1/event-types/{slug}/slots
// Query params:
//
//	from=YYYY-MM-DD  (default: today)
//	to=YYYY-MM-DD    (default: today + max_future_days)
//	tz=IANA/Zone     (default: UTC)
func (h *Handler) GetSlots(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	tzName := r.URL.Query().Get("tz")
	// The public booking page is the one surface that may see taken slots, and only
	// when the event type opted in; computeSlots enforces the second half.
	res, err := h.computeSlots(r.Context(), slug, tzName,
		r.URL.Query().Get("from"), r.URL.Query().Get("to"), true)
	switch {
	case errors.Is(err, errEventTypeNotFound):
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	case errors.Is(err, errInvalidTimezone):
		h.writeError(w, http.StatusBadRequest, "invalid tz: "+tzName)
		return
	case errors.Is(err, errBadDateRange):
		h.writeError(w, http.StatusBadRequest, "from/to must be YYYY-MM-DD and from <= to")
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "slots", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	body := map[string]any{"slots": res.Slots, "hosts": res.Hosts}
	// Absent rather than empty when the event type has not opted in, so a client can
	// tell "this event type does not show taken times" from "none are taken today".
	if res.ShowsTaken {
		body["taken"] = res.Taken
	}
	h.writeJSON(w, http.StatusOK, body)
}

// computeSlots returns the bookable slots (and the candidate hosts' display map) for
// an active+public event type over the given optional date range, in tzName. It's
// the shared core behind the REST GetSlots handler and the MCP get_available_slots
// tool. tzName "" → UTC; fromStr/toStr "" → today / the max-future cap. Returns one
// of the sentinel errors above on bad input, or a wrapped error on internal failure.
func (h *Handler) computeSlots(ctx context.Context, slug, tzName, fromStr, toStr string, includeTaken bool) (slotsResult, error) {
	et, err := h.loadBookableEventType(ctx, slug)
	if err != nil {
		return slotsResult{}, err
	}

	if tzName == "" {
		tzName = "UTC"
	}
	bookerTZ, err := time.LoadLocation(tzName)
	if err != nil {
		return slotsResult{}, errInvalidTimezone
	}

	now := time.Now().UTC()
	dateFrom, dateTo, ok := parseDateRangeStr(fromStr, toStr, now, et.MaxFutureDays)
	if !ok {
		return slotsResult{}, errBadDateRange
	}

	// Resolve the host pool for this event type by routing mode. Round-robin
	// offers a slot if any rotation host is free; fixed/collective gate on the
	// required hosts. Archived hosts are already excluded by resolveEventTypeHosts.
	hosts, err := h.resolveEventTypeHosts(ctx, et.ID)
	if err != nil {
		return slotsResult{}, fmt.Errorf("resolve event-type hosts: %w", err)
	}
	// Pool the hosts that gate this event's slots, tagged with the role the engine
	// needs. Round-robin: required (fixed, always attend) + rotation (pick one).
	// fixed/collective: the required hosts (all must be free).
	type poolHost struct{ id, role string }
	var pool []poolHost
	for _, hh := range hosts {
		if et.RoutingMode == "round_robin" {
			if hh.Role == "rotation" || hh.Role == "required" {
				pool = append(pool, poolHost{hh.UserID, hh.Role})
			}
		} else if hh.Role == "required" { // fixed + collective gate on required hosts
			pool = append(pool, poolHost{hh.UserID, hh.Role})
		}
	}
	if len(pool) == 0 {
		// No bookable hosts (e.g. all archived, or a round-robin with no rotation
		// members) — offer nothing rather than erroring.
		return slotsResult{Slots: []slotJSON{}, Hosts: map[string]map[string]string{}}, nil
	}

	// Load each host's availability concurrently. The slow part is the Google
	// Calendar free/busy round-trip (one per host); fetching them in parallel turns
	// N sequential network calls into ~one call's latency. The DB queries inside
	// serialize on the single-connection pool (fast) — only the network overlaps.
	hostAvails := make([]slots.HostAvailability, len(pool))
	errsByHost := make([]error, len(pool))
	var wg sync.WaitGroup
	for i, ph := range pool {
		wg.Add(1)
		go func(i int, ph poolHost) {
			defer wg.Done()
			ha, err := h.hostAvailability(ctx, ph.id, et.ID, dateFrom, dateTo)
			if err != nil {
				errsByHost[i] = err
				return
			}
			ha.Role = ph.role
			hostAvails[i] = ha
		}(i, ph)
	}
	wg.Wait()
	for i, err := range errsByHost {
		if err != nil {
			return slotsResult{}, fmt.Errorf("load host availability (host %s): %w", pool[i].id, err)
		}
	}

	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     et.DurationMinutes,
			SlotIntervalMinutes: et.SlotIntervalMinutes,
			BufferBeforeMinutes: et.BufferBeforeMinutes,
			BufferAfterMinutes:  et.BufferAfterMinutes,
			MinNoticeMinutes:    et.MinNoticeMinutes,
			MaxFutureDays:       et.MaxFutureDays,
			RoutingMode:         et.RoutingMode,
		},
		Hosts:    hostAvails,
		DateFrom: dateFrom,
		DateTo:   dateTo,
		BookerTZ: bookerTZ,
		Now:      now,
	}

	// Taken slots are produced only when the caller asked for them AND this event type
	// opted in. GenerateWithTaken walks the range a second time with busy ignored, so
	// it is not free, and it returns exactly the information the default must withhold.
	showsTaken := includeTaken && et.ShowTakenSlots
	var result, takenSlots []slots.Slot
	if showsTaken {
		result, takenSlots, err = slots.GenerateWithTaken(req)
	} else {
		result, err = slots.Generate(req)
	}
	if err != nil {
		return slotsResult{}, fmt.Errorf("slots generate: %w", err)
	}

	// Host metadata (name + avatar) for the candidate pool, so the booking page can
	// show whose face goes with each slot's host_ids (round-robin: the priority pick;
	// group: every required host).
	poolIDs := make([]string, len(pool))
	for i, ph := range pool {
		poolIDs[i] = ph.id
	}
	return slotsResult{
		Slots:      toSlotJSON(result),
		Taken:      toSlotJSON(takenSlots),
		Hosts:      h.hostDisplayMap(ctx, poolIDs),
		ShowsTaken: showsTaken,
	}, nil
}

// toSlotJSON renders engine slots for the wire. Taken slots carry no host ids, so
// host_ids is simply absent for them rather than invented.
func toSlotJSON(in []slots.Slot) []slotJSON {
	out := make([]slotJSON, len(in))
	for i, s := range in {
		out[i] = slotJSON{
			Start:   s.Start.Format(time.RFC3339),
			End:     s.End.Format(time.RFC3339),
			HostIDs: s.HostIDs,
		}
	}
	return out
}

// hostDisplayMap returns id → {name, avatar_url} for the given users, for rendering
// host faces on the public booking page.
func (h *Handler) hostDisplayMap(ctx context.Context, ids []string) map[string]map[string]string {
	out := map[string]map[string]string{}
	if len(ids) == 0 {
		return out
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(avatar_url, '') FROM users WHERE id IN (`+strings.Join(ph, ",")+`)`, args...) // #nosec G202 -- ph is a fixed slice of literal "?" placeholders (one per id above); every value is bound via args..., never concatenated into the SQL text
	if err != nil {
		h.logger.ErrorContext(ctx, "slots: host display map", "error", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, avatar string
		if err := rows.Scan(&id, &name, &avatar); err != nil {
			continue
		}
		out[id] = map[string]string{"name": name, "avatar_url": avatar}
	}
	return out
}

// hostAvailability loads one host's timezone, availability rules, overrides, and
// busy intervals (DB bookings + Google Calendar free/busy) for the date range.
// loadHostSchedule loads a host's timezone plus the raw availability inputs (weekly
// rules + date overrides) that slots.ResolveDayWindows needs for eventTypeID. Shared by
// hostAvailability (slot generation, which layers busy-booking loading on top) and
// booking_handler.go's hostAvailableAt (booking-time validation), so the two can't
// drift. Returns materialized slices, closing each cursor before opening the next — the
// MaxOpenConns(1) pool can't hold two open cursors at once (see [[sqlite-single-connection]]).
func (h *Handler) loadHostSchedule(ctx context.Context, userID, eventTypeID string) (*time.Location, []slots.AvailabilityRule, []slots.AvailabilityOverride, error) {
	var hostTZName string
	if err := h.db.QueryRowContext(ctx,
		`SELECT iana_timezone FROM users WHERE id = ?`, userID).Scan(&hostTZName); err != nil {
		return nil, nil, nil, err
	}
	hostLoc, err := time.LoadLocation(hostTZName)
	if err != nil {
		hostLoc = time.UTC
	}

	ruleRows, err := h.db.QueryContext(ctx, `
		SELECT day_of_week, start_time, end_time
		FROM availability_rules
		WHERE user_id = ? AND (event_type_id = ? OR event_type_id IS NULL)
		ORDER BY day_of_week, start_time`, userID, eventTypeID)
	if err != nil {
		return nil, nil, nil, err
	}
	var rules []slots.AvailabilityRule
	for ruleRows.Next() {
		var dow int
		var start, end string
		if err := ruleRows.Scan(&dow, &start, &end); err != nil {
			ruleRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, nil, nil, err
		}
		rules = append(rules, slots.AvailabilityRule{DayOfWeek: time.Weekday(dow), StartTime: start, EndTime: end})
	}
	ruleRows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := ruleRows.Err(); err != nil {
		return nil, nil, nil, err
	}

	ovRows, err := h.db.QueryContext(ctx, `
		SELECT date, is_available, COALESCE(start_time,''), COALESCE(end_time,'')
		FROM availability_overrides WHERE user_id = ?`, userID)
	if err != nil {
		return nil, nil, nil, err
	}
	var overrides []slots.AvailabilityOverride
	for ovRows.Next() {
		var dateStr string
		var isAvail int
		var startT, endT string
		if err := ovRows.Scan(&dateStr, &isAvail, &startT, &endT); err != nil {
			ovRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, nil, nil, err
		}
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		overrides = append(overrides, slots.AvailabilityOverride{Date: date, IsAvailable: isAvail != 0, StartTime: startT, EndTime: endT})
	}
	ovRows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := ovRows.Err(); err != nil {
		return nil, nil, nil, err
	}

	return hostLoc, rules, overrides, nil
}

func (h *Handler) hostAvailability(ctx context.Context, userID, eventTypeID string, dateFrom, dateTo time.Time) (slots.HostAvailability, error) {
	hostLoc, rules, overrides, err := h.loadHostSchedule(ctx, userID, eventTypeID)
	if err != nil {
		return slots.HostAvailability{}, err
	}

	// Widen the busy window by a day on each side. Slots are generated for
	// host-local days, but bookings are stored in UTC — a morning slot for a
	// positive-UTC-offset host (e.g. NZ) maps to the *previous* UTC day, so a
	// tight [dateFrom, dateTo] UTC window would miss the booking that blocks it
	// and the slot would be wrongly offered (then 409 at booking time).
	// Over-fetching is harmless: the engine only subtracts busy that overlaps.
	busyFrom := dateFrom.Add(-24 * time.Hour).Format(time.RFC3339)
	busyTo := dateTo.Add(48 * time.Hour).Format(time.RFC3339)
	// Count every booking this host attends (primary OR a Group/fixed-host seat) as
	// busy — join booking_hosts rather than matching bookings.host_id, so a host on
	// a multi-host call isn't offered an overlapping slot on another event.
	busyRows, err := h.db.QueryContext(ctx, `
		SELECT b.start_at, b.end_at FROM bookings b
		JOIN booking_hosts bh ON bh.booking_id = b.id
		WHERE bh.user_id = ? AND b.status != 'cancelled'
		  AND b.start_at >= ? AND b.start_at < ?`,
		userID, busyFrom, busyTo)
	if err != nil {
		return slots.HostAvailability{}, err
	}
	defer busyRows.Close()
	var busy []slots.Interval
	for busyRows.Next() {
		var startStr, endStr string
		if err := busyRows.Scan(&startStr, &endStr); err != nil {
			return slots.HostAvailability{}, err
		}
		s, err1 := time.Parse(time.RFC3339Nano, startStr)
		e, err2 := time.Parse(time.RFC3339Nano, endStr)
		if err1 != nil || err2 != nil {
			continue
		}
		busy = append(busy, slots.Interval{Start: s, End: e})
	}
	if err := busyRows.Err(); err != nil {
		return slots.HostAvailability{}, err
	}

	// Calnode's own events on this host's calendar also show up in Google free/busy,
	// but the DB query above is the source of truth for them (§6.2) — so subtract
	// them from the free/busy result. This removes the double-count for confirmed
	// bookings and, crucially, stops a *cancelled* booking whose Google event hasn't
	// been deleted yet from blocking the freed slot. external_event_id is non-empty
	// only while we believe a Google event still exists (it's cleared on a successful
	// cancel, inline or via the reconciler), so this targets exactly our own events.
	// Materialise fully before the free/busy call (single-connection pool).
	var ownEvents []slots.Interval
	ownRows, err := h.db.QueryContext(ctx, `
		SELECT b.start_at, b.end_at FROM bookings b
		JOIN booking_hosts bh ON bh.booking_id = b.id
		WHERE bh.user_id = ? AND COALESCE(bh.external_event_id, '') != ''
		  AND b.start_at >= ? AND b.start_at < ?`,
		userID, busyFrom, busyTo)
	if err != nil {
		return slots.HostAvailability{}, err
	}
	for ownRows.Next() {
		var startStr, endStr string
		if err := ownRows.Scan(&startStr, &endStr); err != nil {
			ownRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return slots.HostAvailability{}, err
		}
		s, err1 := time.Parse(time.RFC3339Nano, startStr)
		e, err2 := time.Parse(time.RFC3339Nano, endStr)
		if err1 != nil || err2 != nil {
			continue
		}
		ownEvents = append(ownEvents, slots.Interval{Start: s, End: e})
	}
	ownRows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := ownRows.Err(); err != nil {
		return slots.HostAvailability{}, err
	}

	// Merge Google Calendar free/busy (check_conflicts connections only), minus our
	// own events. Non-fatal.
	if gc := h.getCal(); gc != nil {
		if gcalBusy, err := gc.FreeBusy(ctx, userID, dateFrom, dateTo.Add(24*time.Hour)); err != nil {
			h.logger.ErrorContext(ctx, "slots: gcal freebusy", "error", err, "host", userID)
		} else {
			busy = append(busy, slots.SubtractIntervals(gcalBusy, ownEvents)...)
		}
	}

	return slots.HostAvailability{HostID: userID, Location: hostLoc, Rules: rules, Overrides: overrides, Busy: busy}, nil
}

// parseDateRangeStr resolves from/to date strings (either may be "") to UTC-midnight times.
// Returns (from, to, ok). ok=false means the params were malformed.
// maxFutureDays=0 is treated as 365 (no configured limit). The resolved
// cap is always enforced on the to= param to prevent CPU-DoS via far-future dates.
func parseDateRangeStr(fromStr, toStr string, now time.Time, maxFutureDays int) (time.Time, time.Time, bool) {
	today := now.UTC().Truncate(24 * time.Hour)

	// Mirror generate.go: 0 means "no configured limit"; use 365 as the cap.
	effectiveMax := maxFutureDays
	if effectiveMax <= 0 {
		effectiveMax = 365
	}
	cap := today.AddDate(0, 0, effectiveMax)

	var dateFrom, dateTo time.Time
	var err error

	if fromStr == "" {
		dateFrom = today
	} else {
		dateFrom, err = time.Parse("2006-01-02", fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
	}
	if toStr == "" {
		dateTo = cap
	} else {
		dateTo, err = time.Parse("2006-01-02", toStr)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		// Clamp caller-supplied to= against the cap to prevent DoS.
		if dateTo.After(cap) {
			dateTo = cap
		}
	}
	if dateTo.Before(dateFrom) {
		return time.Time{}, time.Time{}, false
	}
	return dateFrom, dateTo, true
}
