package slots

import (
	"sort"
	"time"
)

// HostAvailability is everything needed to compute one host's free windows.
type HostAvailability struct {
	HostID    string
	Location  *time.Location     // IANA timezone
	Rules     []AvailabilityRule // weekly recurring rules
	Overrides []AvailabilityOverride
	// Busy holds active bookings + external-calendar busy intervals for this
	// host.  Calnode-tagged events must already be excluded by the caller (§6.2).
	Busy []Interval
}

// EventConfig holds the event-type parameters that govern slot generation.
type EventConfig struct {
	DurationMinutes     int
	SlotIntervalMinutes int
	BufferBeforeMinutes int
	BufferAfterMinutes  int
	MinNoticeMinutes    int
	MaxFutureDays       int
	// RoutingMode: "fixed" | "round_robin" | "collective" | "priority"
	RoutingMode string
}

// Slot is one bookable time window rendered for the booker.
type Slot struct {
	Start  time.Time
	End    time.Time
	// HostID is the assigned host.  For round_robin the actual assignment
	// happens inside the booking transaction (§6.4, §7); this field holds
	// the first available host as a placeholder for display purposes.
	HostID string
}

// Request is the complete input to Generate.
type Request struct {
	Event    EventConfig
	Hosts    []HostAvailability
	DateFrom time.Time      // inclusive; only date portion is used
	DateTo   time.Time      // inclusive; only date portion is used
	BookerTZ *time.Location // output timezone for slot Start/End
	Now      time.Time      // injectable clock; use time.Now().UTC() in production
}

// Generate runs the slot-generation algorithm (§9) and returns bookable slots
// rendered in the booker's timezone, ordered by start time.
func Generate(req Request) ([]Slot, error) {
	dur := time.Duration(req.Event.DurationMinutes) * time.Minute
	interval := time.Duration(req.Event.SlotIntervalMinutes) * time.Minute
	bufBefore := time.Duration(req.Event.BufferBeforeMinutes) * time.Minute
	bufAfter := time.Duration(req.Event.BufferAfterMinutes) * time.Minute
	minNotice := req.Now.Add(time.Duration(req.Event.MinNoticeMinutes) * time.Minute)
	maxFuture := req.Now.Add(time.Duration(req.Event.MaxFutureDays) * 24 * time.Hour)

	// perStart[slotStartUTC] = set of host IDs that have that start free.
	type hostSet map[string]bool
	perStart := make(map[time.Time]hostSet)

	for d := req.DateFrom; !d.After(req.DateTo); d = d.AddDate(0, 0, 1) {
		for _, host := range req.Hosts {
			windows, err := resolveDay(host.Location, d, host.Rules, host.Overrides)
			if err != nil {
				return nil, err
			}
			if len(windows) == 0 {
				continue
			}

			busy := expandBusy(host.Busy, bufBefore, bufAfter)
			free := subtract(windows, busy)

			for _, f := range free {
				// Align the first slot start up to the nearest interval boundary
				// (epoch-aligned so slots land on :00/:15/:30/:45 etc.).
				t := alignUp(f.Start, interval)
				for ; !t.Add(dur).After(f.End); t = t.Add(interval) {
					if t.Before(minNotice) {
						continue
					}
					if t.After(maxFuture) {
						break
					}
					if perStart[t] == nil {
						perStart[t] = make(hostSet)
					}
					perStart[t][host.HostID] = true
				}
			}
		}
	}

	// Collect and sort candidate start times.
	starts := make([]time.Time, 0, len(perStart))
	for t := range perStart {
		starts = append(starts, t)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	// Apply routing mode to decide which slots to surface.
	slots := make([]Slot, 0, len(starts))
	for _, t := range starts {
		hostID := pickHost(req.Hosts, perStart[t], req.Event.RoutingMode)
		if hostID == "" {
			continue
		}
		slots = append(slots, Slot{
			Start:  t.In(req.BookerTZ),
			End:    t.Add(dur).In(req.BookerTZ),
			HostID: hostID,
		})
	}
	return slots, nil
}

// pickHost applies routing mode logic and returns the host to surface for a
// slot, or "" if the slot should not be offered.
//
// Round-robin actual assignment happens at booking time (§6.4, §7); here we
// return the first free host as a display placeholder.
func pickHost(hosts []HostAvailability, available map[string]bool, mode string) string {
	switch mode {
	case "collective":
		// Slot is only offered when every host is free.
		for _, h := range hosts {
			if !available[h.HostID] {
				return ""
			}
		}
		if len(hosts) == 0 {
			return ""
		}
		return hosts[0].HostID

	case "priority":
		// First available host in priority order (caller orders hosts by routing_priority).
		for _, h := range hosts {
			if available[h.HostID] {
				return h.HostID
			}
		}
		return ""

	case "round_robin":
		// Slot offered if any host is free.
		for _, h := range hosts {
			if available[h.HostID] {
				return h.HostID
			}
		}
		return ""

	default: // "fixed" and fallback
		if len(hosts) == 0 {
			return ""
		}
		h := hosts[0]
		if available[h.HostID] {
			return h.HostID
		}
		return ""
	}
}

// alignUp rounds t up to the next epoch-aligned multiple of interval.
// Epoch alignment means slots land on :00/:15/:30/:45 for minute-granularity
// intervals, regardless of when the free window starts.
func alignUp(t time.Time, interval time.Duration) time.Time {
	secs := int64(interval.Seconds())
	if secs <= 0 {
		return t
	}
	unix := t.Unix()
	rem := unix % secs
	if rem == 0 {
		return t
	}
	return t.Add(time.Duration(secs-rem) * time.Second)
}
