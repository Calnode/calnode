package slots

import (
	"fmt"
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
	// Role is the host's role for round_robin events: "required" (a fixed host who
	// always attends — must be free for the slot to be offered) or "rotation" (the
	// pool one is picked from). Empty for other modes, which don't split by role.
	Role string
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
	Start time.Time
	End   time.Time
	// HostIDs contains the assigned host(s). For fixed/round_robin/priority
	// this is always a single-element slice. For collective all participating
	// hosts are listed — the booking layer must create attendee records for each.
	HostIDs []string
}

// Request is the complete input to Generate.
type Request struct {
	Event    EventConfig
	Hosts    []HostAvailability
	DateFrom time.Time      // inclusive; only the UTC date portion is used
	DateTo   time.Time      // inclusive; only the UTC date portion is used
	BookerTZ *time.Location // output timezone for slot Start/End; must not be nil
	Now      time.Time      // injectable clock; use time.Now().UTC() in production
}

// Generate runs the slot-generation algorithm (§9) and returns bookable slots
// rendered in the booker's timezone, ordered by start time.
func Generate(req Request) ([]Slot, error) {
	res, err := generate(req, Extras{})
	return res.Free, err
}

// GenerateWithTaken runs the same algorithm and additionally reports the starts a
// booking or calendar conflict took away, for event types that show those greyed out
// rather than hiding them.
//
// "Taken" is defined by difference rather than by inspecting reasons: a start is taken
// if it WOULD have been offered with no busy intervals at all, and is not offered with
// the real ones. That definition is what makes it correct for every routing mode
// without special-casing any of them, and it is why a start outside the host's working
// hours, one removed by the minimum-notice rule, or one lost to a host pool that cannot
// satisfy the routing mode can never be reported as taken: those are absent from both
// passes, so they cancel out.
//
// That distinction is the whole point. Greying a slot says "somebody booked this", and
// saying it about a time the host simply does not work would be worse than showing
// nothing at all.
//
// Buffers do count as taken, deliberately: a start inside the buffer around an adjacent
// meeting genuinely cannot be booked, and the booker has no way to tell that apart from
// the meeting itself.
//
// Taken slots carry no HostIDs. The caller only needs the time, and naming the host
// would say which specific person is busy, which is more than the feature needs to
// disclose.
func GenerateWithTaken(req Request) (free, taken []Slot, err error) {
	res, err := generate(req, Extras{Taken: true})
	return res.Free, res.Taken, err
}

// Extras selects the optional secondary outputs of a pass. Each one costs work, so
// every caller says what it needs (see GenerateDetailed).
type Extras struct {
	// Taken asks for the starts a booking or calendar conflict removed. Costs a second
	// walk of the range.
	Taken bool
	// NoticeGap asks for the starts the minimum-notice rule removed. Free when the event
	// type sets no minimum notice, and otherwise one extra map plus a routing pass over
	// the handful of starts involved — it is collected during the main walk.
	NoticeGap bool
}

// Result is one pass's output: the bookable slots, plus whichever secondary outputs the
// caller asked for.
type Result struct {
	// Free is the bookable slots, ordered by start time.
	Free []Slot
	// Taken is the starts a booking or calendar conflict removed (see GenerateWithTaken).
	Taken []Slot
	// NoticeGap is the starts the minimum-notice rule removed: they are inside the host's
	// working hours, nothing is booked over them, and the routing mode could have been
	// satisfied — they are simply too soon. It exists so a booking surface can say WHY
	// the nearest times are missing instead of leaving the visitor to guess, which is the
	// single most common "why can't I see those times" question (#20).
	//
	// Two exclusions make the answer honest rather than merely plausible:
	//
	//   - A start already in the past is never reported. It would have been dropped with
	//     no notice policy at all, so attributing it to the policy would be a lie that
	//     puts the message on every event type by dinnertime.
	//   - A start a booking took away is never reported, because busy intervals are
	//     applied on the way in. It follows that NoticeGap and Taken are disjoint, so a
	//     surface can render both without ever explaining one start two ways.
	//
	// Like Taken, these carry no HostIDs: the caller needs the time, not who was free.
	NoticeGap []Slot
}

// GenerateDetailed is Generate with the secondary outputs in Extras. One call so a
// surface that wants both (an event type showing taken times AND setting a minimum
// notice) doesn't walk the range twice for them.
func GenerateDetailed(req Request, want Extras) (Result, error) {
	return generate(req, want)
}

// params holds the derived scalars every pass needs, computed once.
type params struct {
	dur              time.Duration
	interval         time.Duration
	bufBefore        time.Duration
	bufAfter         time.Duration
	minNotice        time.Time
	maxFuture        time.Time
	dateFrom, dateTo time.Time
}

func newParams(req Request) params {
	p := params{
		dur:       time.Duration(req.Event.DurationMinutes) * time.Minute,
		interval:  time.Duration(req.Event.SlotIntervalMinutes) * time.Minute,
		bufBefore: time.Duration(req.Event.BufferBeforeMinutes) * time.Minute,
		bufAfter:  time.Duration(req.Event.BufferAfterMinutes) * time.Minute,
		minNotice: req.Now.Add(time.Duration(req.Event.MinNoticeMinutes) * time.Minute),
		// Truncate to UTC midnight so weekday matching and date arithmetic are
		// consistent regardless of what time-of-day the caller passes.
		dateFrom: req.DateFrom.UTC().Truncate(24 * time.Hour),
		dateTo:   req.DateTo.UTC().Truncate(24 * time.Hour),
	}
	if req.Event.MaxFutureDays > 0 {
		p.maxFuture = req.Now.Add(time.Duration(req.Event.MaxFutureDays) * 24 * time.Hour)
	} else {
		p.maxFuture = req.Now.Add(365 * 24 * time.Hour) // 0 = no configured limit; use 1-year guard
	}
	return p
}

func generate(req Request, want Extras) (Result, error) {
	if req.Event.DurationMinutes <= 0 {
		return Result{}, fmt.Errorf("slots: DurationMinutes must be positive")
	}
	if req.Event.SlotIntervalMinutes <= 0 {
		return Result{}, fmt.Errorf("slots: SlotIntervalMinutes must be positive")
	}
	if req.BookerTZ == nil {
		return Result{}, fmt.Errorf("slots: BookerTZ must not be nil")
	}
	for i, h := range req.Hosts {
		if h.Location == nil {
			return Result{}, fmt.Errorf("slots: Hosts[%d] (%s) Location must not be nil", i, h.HostID)
		}
	}
	p := newParams(req)

	// Collected during the main walk rather than by a second pass: the notice cutoff is
	// evaluated there anyway, so the starts it drops are already in hand. nil switches
	// the collection off, which is also what happens when the event type sets no minimum
	// notice — there is then no policy to attribute anything to.
	var belowNotice map[time.Time]map[string]bool
	if want.NoticeGap && req.Event.MinNoticeMinutes > 0 {
		belowNotice = make(map[time.Time]map[string]bool)
	}

	perStart, err := hostsByStart(req, p, true, belowNotice)
	if err != nil {
		return Result{}, err
	}
	res := Result{Free: offer(req, p, perStart)}

	// Run the same routing rules over the withheld starts, so only the ones that really
	// would have been offered are reported: a start no host pool could satisfy is not the
	// notice policy's fault.
	for _, s := range offer(req, p, belowNotice) {
		res.NoticeGap = append(res.NoticeGap, Slot{Start: s.Start, End: s.End})
	}

	if !want.Taken {
		return res, nil
	}

	// The same walk with every host's busy list ignored: what the calendar would offer
	// if nothing were booked.
	perStartIgnoringBusy, err := hostsByStart(req, p, false, nil)
	if err != nil {
		return Result{}, err
	}
	offered := make(map[time.Time]bool, len(res.Free))
	for _, s := range res.Free {
		offered[s.Start] = true
	}
	for _, s := range offer(req, p, perStartIgnoringBusy) {
		if !offered[s.Start] {
			res.Taken = append(res.Taken, Slot{Start: s.Start, End: s.End})
		}
	}
	return res, nil
}

// hostsByStart walks every candidate start in the range and records which hosts have it
// free. applyBusy=false ignores the busy lists entirely, which is how the "nothing is
// booked" comparison pass is built. A non-nil belowNotice collects the starts the
// minimum-notice rule removed, in the same shape.
func hostsByStart(req Request, p params, applyBusy bool, belowNotice map[time.Time]map[string]bool) (map[time.Time]map[string]bool, error) {
	perStart := make(map[time.Time]map[string]bool)

	for d := p.dateFrom; !d.After(p.dateTo); d = d.AddDate(0, 0, 1) {
		for _, host := range req.Hosts {
			windows, err := resolveDay(host.Location, d, host.Rules, host.Overrides)
			if err != nil {
				return nil, err
			}
			if len(windows) == 0 {
				continue
			}

			avail := windows
			if applyBusy {
				avail = subtract(windows, expandBusy(host.Busy, p.bufBefore, p.bufAfter))
			}

			for _, f := range avail {
				// Align the first slot start up to the nearest interval boundary
				// (epoch-aligned so slots land on :00/:15/:30/:45 etc.).
				t := alignUp(f.Start, p.interval)
				for ; !t.Add(p.dur).After(f.End); t = t.Add(p.interval) {
					if t.Before(p.minNotice) {
						// !t.Before(req.Now) is what keeps the attribution honest: a start
						// in the past would have gone with no notice policy at all, so
						// blaming the policy for it would put the explanation on every
						// event type by the end of the working day.
						if belowNotice != nil && !t.Before(req.Now) {
							if belowNotice[t] == nil {
								belowNotice[t] = make(map[string]bool)
							}
							belowNotice[t][host.HostID] = true
						}
						continue
					}
					if t.After(p.maxFuture) {
						break
					}
					if perStart[t] == nil {
						perStart[t] = make(map[string]bool)
					}
					perStart[t][host.HostID] = true
				}
			}
		}
	}
	return perStart, nil
}

// offer applies the routing mode to decide which starts to surface, in order.
func offer(req Request, p params, perStart map[time.Time]map[string]bool) []Slot {
	starts := make([]time.Time, 0, len(perStart))
	for t := range perStart {
		starts = append(starts, t)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	out := make([]Slot, 0, len(starts))
	for _, t := range starts {
		hostIDs := pickHosts(req.Hosts, perStart[t], req.Event.RoutingMode)
		if len(hostIDs) == 0 {
			continue
		}
		out = append(out, Slot{
			Start:   t.In(req.BookerTZ),
			End:     t.Add(p.dur).In(req.BookerTZ),
			HostIDs: hostIDs,
		})
	}
	return out
}

// pickHosts applies routing mode logic and returns the host(s) to surface for a
// slot, or nil if the slot should not be offered.
//
// For collective mode all host IDs are returned — the booking layer must assign
// all of them. For other modes a single host ID is returned as a one-element
// slice. Round-robin actual assignment happens at booking time (§6.4, §7).
func pickHosts(hosts []HostAvailability, available map[string]bool, mode string) []string {
	switch mode {
	case "collective":
		// Slot is only offered when every host is free.
		if len(hosts) == 0 {
			return nil
		}
		for _, h := range hosts {
			if !available[h.HostID] {
				return nil
			}
		}
		ids := make([]string, len(hosts))
		for i, h := range hosts {
			ids[i] = h.HostID
		}
		return ids

	case "priority":
		// First available host in priority order (caller orders hosts by routing_priority).
		for _, h := range hosts {
			if available[h.HostID] {
				return []string{h.HostID}
			}
		}
		return nil

	case "round_robin":
		// Fixed "required" hosts (if any) must all be free and always attend; one
		// "rotation" host is picked at booking time. Offer the slot only when every
		// required host is free AND ≥1 rotation host is free. Hosts with no role tag
		// are treated as rotation (back-compat with rotation-only round robin).
		var out []string
		for _, h := range hosts {
			if h.Role == "required" {
				if !available[h.HostID] {
					return nil // a fixed host is busy — slot not offered
				}
				out = append(out, h.HostID)
			}
		}
		// A round-robin booking ALWAYS assigns exactly one rotation host, so the
		// slot is offered only when one is free. If there's no rotation pool at all
		// the event is misconfigured for round_robin (it's really a Group) — don't
		// offer, to stay consistent with booking-time assignment which would 409.
		rotationFree := false
		for _, h := range hosts {
			if h.Role == "required" {
				continue
			}
			if available[h.HostID] {
				out = append(out, h.HostID) // first available rotation host (priority order)
				rotationFree = true
				break
			}
		}
		if !rotationFree {
			return nil
		}
		return out

	default: // "fixed" and fallback
		if len(hosts) == 0 {
			return nil
		}
		h := hosts[0]
		if available[h.HostID] {
			return []string{h.HostID}
		}
		return nil
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
	if rem < 0 {
		rem += secs // Go's % returns negative remainder for negative dividend
	}
	if rem == 0 {
		return t
	}
	return t.Add(time.Duration(secs-rem) * time.Second)
}
