package slots_test

import (
	"testing"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// hhmm renders a slot start as "15:04" UTC, which is what these assertions read as.
func hhmm(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.UTC().Format("15:04")
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// takenReq builds a one-Monday request with a 09:00-12:00 window and 60-minute slots,
// so the untouched calendar offers exactly 09:00, 10:00 and 11:00.
func takenReq(hosts []slots.HostAvailability, mode string) slots.Request {
	return slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     60,
			SlotIntervalMinutes: 60,
			RoutingMode:         mode,
			MaxFutureDays:       30,
		},
		Hosts:    hosts,
		DateFrom: utcDate(2026, 6, 15), // Monday
		DateTo:   utcDate(2026, 6, 15),
		BookerTZ: time.UTC,
		Now:      utcTime(2026, 6, 14, 0, 0, 0), // the day before; nothing near min-notice
	}
}

// TestGenerateWithTaken_reportsBookedStarts is the core of issue #19: a start removed
// by a booking is reported so the page can grey it out instead of hiding it.
func TestGenerateWithTaken_reportsBookedStarts(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	host := singleHost("h1", time.UTC, monRules("09:00", "12:00"), busyUTC(10, 0, 11, 0, mon))

	free, taken, err := slots.GenerateWithTaken(takenReq([]slots.HostAvailability{host}, "fixed"))
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if got, want := hhmm(startTimes(free)), []string{"09:00", "11:00"}; !equalStrings(got, want) {
		t.Errorf("free = %v, want %v", got, want)
	}
	if got, want := hhmm(startTimes(taken)), []string{"10:00"}; !equalStrings(got, want) {
		t.Errorf("taken = %v, want %v - the booked hour is what gets greyed out", got, want)
	}
}

// Generate must be untouched by any of this. It is the path every existing caller uses
// and the one the public API still defaults to.
func TestGenerate_unchangedByTakenSupport(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	host := singleHost("h1", time.UTC, monRules("09:00", "12:00"), busyUTC(10, 0, 11, 0, mon))
	req := takenReq([]slots.HostAvailability{host}, "fixed")

	plain, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	free, _, err := slots.GenerateWithTaken(req)
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if !equalStrings(hhmm(startTimes(plain)), hhmm(startTimes(free))) {
		t.Errorf("Generate gave %v but GenerateWithTaken gave %v; the bookable set must be identical",
			hhmm(startTimes(plain)), hhmm(startTimes(free)))
	}
}

// TestGenerateWithTaken_neverReportsTimesOutsideWorkingHours is the assertion that
// makes the feature safe to ship. Greying a slot tells the booker "somebody booked
// this". Saying that about a time the host simply does not work would both mislead
// them and expose the shape of the working day, which is the objection that nearly
// sank the whole idea in discussion #14.
func TestGenerateWithTaken_neverReportsTimesOutsideWorkingHours(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	// Works 09:00-12:00 only. 08:00 and 13:00 are outside it and must not appear.
	host := singleHost("h1", time.UTC, monRules("09:00", "12:00"), busyUTC(10, 0, 11, 0, mon))

	_, taken, err := slots.GenerateWithTaken(takenReq([]slots.HostAvailability{host}, "fixed"))
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	for _, s := range hhmm(startTimes(taken)) {
		if s < "09:00" || s >= "12:00" {
			t.Errorf("taken included %s, which is outside the host's hours; "+
				"greying it would claim a booking exists and leak the working day", s)
		}
	}
}

// A start held back only by the minimum-notice rule must not be reported as taken:
// nobody booked it, and saying so would corrupt exactly the signal this feature
// exists to send. It is explained in words on the page instead (#20).
func TestGenerateWithTaken_minNoticeIsNotTaken(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	req := takenReq(
		[]slots.HostAvailability{singleHost("h1", time.UTC, monRules("09:00", "12:00"))},
		"fixed")
	// Now is 08:00 that same morning with four hours' notice required, so 09:00 and
	// 11:00 fall inside the notice window while nothing at all is booked.
	req.Now = utcTime(2026, 6, 15, 8, 0, 0)
	req.Event.MinNoticeMinutes = 240
	_ = mon

	free, taken, err := slots.GenerateWithTaken(req)
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if len(taken) != 0 {
		t.Errorf("taken = %v, want none: no booking exists, the notice rule withheld these",
			hhmm(startTimes(taken)))
	}
	// Sanity: the notice rule really did remove something, or the test proves nothing.
	if len(free) >= 3 {
		t.Fatalf("free = %v; expected the notice rule to withhold earlier starts, "+
			"otherwise this test would pass even if min-notice were reported as taken",
			hhmm(startTimes(free)))
	}
}

// Buffers count as taken on purpose. The start really is unbookable, and from the
// booker's side it is indistinguishable from the meeting that caused it.
func TestGenerateWithTaken_bufferBlockedIsTaken(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	host := singleHost("h1", time.UTC, monRules("09:00", "12:00"), busyUTC(11, 0, 12, 0, mon))
	req := takenReq([]slots.HostAvailability{host}, "fixed")
	req.Event.BufferBeforeMinutes = 30 // pushes the block back over the 10:00 start

	free, taken, err := slots.GenerateWithTaken(req)
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	gotTaken := hhmm(startTimes(taken))
	if !equalStrings(gotTaken, []string{"10:00", "11:00"}) {
		t.Errorf("taken = %v, want [10:00 11:00]: 11:00 is the meeting and 10:00 is the "+
			"buffer in front of it, which is equally unbookable", gotTaken)
	}
	if got := hhmm(startTimes(free)); !equalStrings(got, []string{"09:00"}) {
		t.Errorf("free = %v, want [09:00]", got)
	}
}

// Round robin: a start is only taken when the booking actually removed it. With two
// rotation hosts and one of them busy, the slot is still offered, so nothing is taken.
// Defining "taken" by difference is what gets this right without special-casing modes.
func TestGenerateWithTaken_roundRobinOnlyWhenNoHostIsLeft(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	rules := monRules("09:00", "12:00")

	oneBusy := []slots.HostAvailability{
		{HostID: "h1", Location: time.UTC, Rules: rules, Busy: []slots.Interval{busyUTC(10, 0, 11, 0, mon)}, Role: "rotation"},
		{HostID: "h2", Location: time.UTC, Rules: rules, Role: "rotation"},
	}
	free, taken, err := slots.GenerateWithTaken(takenReq(oneBusy, "round_robin"))
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if len(taken) != 0 {
		t.Errorf("taken = %v, want none: h2 is still free at that hour so it is bookable",
			hhmm(startTimes(taken)))
	}
	if got, want := hhmm(startTimes(free)), []string{"09:00", "10:00", "11:00"}; !equalStrings(got, want) {
		t.Errorf("free = %v, want %v", got, want)
	}

	// Both busy at 10:00: now the hour genuinely is gone, and it is reported.
	bothBusy := []slots.HostAvailability{
		{HostID: "h1", Location: time.UTC, Rules: rules, Busy: []slots.Interval{busyUTC(10, 0, 11, 0, mon)}, Role: "rotation"},
		{HostID: "h2", Location: time.UTC, Rules: rules, Busy: []slots.Interval{busyUTC(10, 0, 11, 0, mon)}, Role: "rotation"},
	}
	_, taken, err = slots.GenerateWithTaken(takenReq(bothBusy, "round_robin"))
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if got, want := hhmm(startTimes(taken)), []string{"10:00"}; !equalStrings(got, want) {
		t.Errorf("taken = %v, want %v", got, want)
	}
}

// Taken slots must not name a host. The caller needs the time; saying who is busy
// discloses more about an individual than greying the slot requires.
func TestGenerateWithTaken_takenSlotsCarryNoHost(t *testing.T) {
	mon := utcDate(2026, 6, 15)
	host := singleHost("h1", time.UTC, monRules("09:00", "12:00"), busyUTC(10, 0, 11, 0, mon))

	_, taken, err := slots.GenerateWithTaken(takenReq([]slots.HostAvailability{host}, "fixed"))
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if len(taken) == 0 {
		t.Fatal("expected a taken slot to assert on")
	}
	for _, s := range taken {
		if len(s.HostIDs) != 0 {
			t.Errorf("taken slot at %s named hosts %v; it should name none",
				s.Start.Format("15:04"), s.HostIDs)
		}
	}
}

// A day the host does not work at all yields nothing, taken included. Otherwise every
// weekend would render as a wall of grey.
func TestGenerateWithTaken_dayWithNoRulesYieldsNothing(t *testing.T) {
	req := takenReq(
		[]slots.HostAvailability{singleHost("h1", time.UTC, monRules("09:00", "12:00"))},
		"fixed")
	req.DateFrom = utcDate(2026, 6, 16) // Tuesday: no rule covers it
	req.DateTo = utcDate(2026, 6, 16)

	free, taken, err := slots.GenerateWithTaken(req)
	if err != nil {
		t.Fatalf("GenerateWithTaken: %v", err)
	}
	if len(free) != 0 || len(taken) != 0 {
		t.Errorf("free = %v, taken = %v; a non-working day should produce neither",
			hhmm(startTimes(free)), hhmm(startTimes(taken)))
	}
}
