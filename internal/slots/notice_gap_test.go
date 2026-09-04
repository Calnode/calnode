package slots_test

import (
	"testing"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// The minimum-notice rule removes the starts nearest to now and leaves nothing behind to
// explain them, which is the most common "why can't I see those times" question (#20).
// GenerateDetailed reports those starts so a booking surface can say so — but only when
// the policy is genuinely the reason, which is what most of these tests pin.

// noticeReq is a wide-open Monday for one host, with now during that Monday morning.
func noticeReq(minNotice int, now time.Time, busy ...slots.Interval) slots.Request {
	date := utcDate(2026, 6, 15) // a Monday
	return slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MinNoticeMinutes:    minNotice,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", time.UTC, monRules("09:00", "17:00"), busy...)},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: time.UTC,
		Now:      now,
	}
}

func TestGenerateDetailed_noticeGapIsExactlyWhatMinNoticeRemoved(t *testing.T) {
	// 09:00, and bookings need 2 hours' notice: 09:00-10:30 are gone, 11:00 is the first
	// bookable start.
	res, err := slots.GenerateDetailed(noticeReq(120, utcTime(2026, 6, 15, 9, 0, 0)), slots.Extras{NoticeGap: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	want := []time.Time{
		utcTime(2026, 6, 15, 9, 0, 0),
		utcTime(2026, 6, 15, 9, 30, 0),
		utcTime(2026, 6, 15, 10, 0, 0),
		utcTime(2026, 6, 15, 10, 30, 0),
	}
	got := startTimes(res.NoticeGap)
	if len(got) != len(want) {
		t.Fatalf("NoticeGap starts: got %v; want %v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("NoticeGap[%d]: got %v; want %v", i, got[i], want[i])
		}
	}
	// And the same starts are absent from Free: the two together account for the day.
	for _, s := range res.Free {
		if s.Start.UTC().Before(utcTime(2026, 6, 15, 11, 0, 0)) {
			t.Errorf("Free contains %v, which min notice should have removed", s.Start)
		}
	}
	if len(res.Free) == 0 {
		t.Error("Free is empty; the fixture is meant to leave the afternoon bookable")
	}
}

func TestGenerateDetailed_noticeGapCarriesNoHostIDs(t *testing.T) {
	// Same reasoning as taken slots: the caller needs the time, and naming who was free
	// at a time nobody can book says more than the feature needs to.
	res, err := slots.GenerateDetailed(noticeReq(120, utcTime(2026, 6, 15, 9, 0, 0)), slots.Extras{NoticeGap: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if len(res.NoticeGap) == 0 {
		t.Fatal("fixture produced no notice gap")
	}
	for _, s := range res.NoticeGap {
		if len(s.HostIDs) != 0 {
			t.Errorf("NoticeGap slot at %v names hosts %v", s.Start, s.HostIDs)
		}
		if !s.End.Equal(s.Start.Add(30 * time.Minute)) {
			t.Errorf("NoticeGap slot at %v has End %v; want start+duration", s.Start, s.End)
		}
	}
}

func TestGenerateDetailed_startsAlreadyPastAreNotBlamedOnTheNoticePolicy(t *testing.T) {
	// 16:00 with 60 minutes' notice. 09:00-15:30 are gone because they are in the PAST,
	// not because of the policy: they would have gone with no policy at all. Only 16:00
	// and 16:30 fall inside the notice window.
	//
	// Getting this wrong would put "bookings must be made 1 hour in advance" on every
	// event type by the end of the working day, which is worse than saying nothing.
	res, err := slots.GenerateDetailed(noticeReq(60, utcTime(2026, 6, 15, 16, 0, 0)), slots.Extras{NoticeGap: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	want := []time.Time{
		utcTime(2026, 6, 15, 16, 0, 0),
		utcTime(2026, 6, 15, 16, 30, 0),
	}
	got := startTimes(res.NoticeGap)
	if len(got) != len(want) {
		t.Fatalf("NoticeGap starts: got %v; want %v (only the starts inside the notice window)", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("NoticeGap[%d]: got %v; want %v", i, got[i], want[i])
		}
	}
}

func TestGenerateDetailed_aBookedStartIsNotBlamedOnTheNoticePolicy(t *testing.T) {
	// 09:00, 2 hours' notice, and 10:00-11:00 is already booked. 10:00 and 10:30 are gone
	// twice over; the honest answer is that they were taken, so they must not appear in
	// the notice gap. Busy intervals are applied on the way in, which is what makes
	// NoticeGap and Taken disjoint.
	req := noticeReq(120, utcTime(2026, 6, 15, 9, 0, 0), busyUTC(10, 0, 11, 0, utcDate(2026, 6, 15)))
	res, err := slots.GenerateDetailed(req, slots.Extras{NoticeGap: true, Taken: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	for _, s := range res.NoticeGap {
		st := s.Start.UTC()
		if st.Equal(utcTime(2026, 6, 15, 10, 0, 0)) || st.Equal(utcTime(2026, 6, 15, 10, 30, 0)) {
			t.Errorf("booked start %v reported as withheld by the notice policy", st)
		}
	}
	// 09:00 and 09:30 are still the policy's doing.
	if len(res.NoticeGap) != 2 {
		t.Errorf("NoticeGap: got %v; want the two unbooked starts inside the notice window", startTimes(res.NoticeGap))
	}
	// Nothing may be explained twice.
	inNotice := map[time.Time]bool{}
	for _, s := range res.NoticeGap {
		inNotice[s.Start.UTC()] = true
	}
	for _, s := range res.Taken {
		if inNotice[s.Start.UTC()] {
			t.Errorf("start %v is reported as both taken and withheld by the notice policy", s.Start)
		}
	}
}

func TestGenerateDetailed_noNoticePolicyMeansNoNoticeGap(t *testing.T) {
	// Nothing to attribute: with no policy, the starts before now are simply past.
	res, err := slots.GenerateDetailed(noticeReq(0, utcTime(2026, 6, 15, 12, 0, 0)), slots.Extras{NoticeGap: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if len(res.NoticeGap) != 0 {
		t.Errorf("NoticeGap with min_notice=0: got %v; want none", startTimes(res.NoticeGap))
	}
}

func TestGenerateDetailed_noticeGapIsOptIn(t *testing.T) {
	// The default Extras asks for nothing, and Generate/GenerateWithTaken keep behaving
	// exactly as before.
	req := noticeReq(120, utcTime(2026, 6, 15, 9, 0, 0))
	res, err := slots.GenerateDetailed(req, slots.Extras{})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if len(res.NoticeGap) != 0 {
		t.Errorf("NoticeGap without Extras.NoticeGap: got %v; want none", startTimes(res.NoticeGap))
	}
	if len(res.Taken) != 0 {
		t.Errorf("Taken without Extras.Taken: got %v; want none", startTimes(res.Taken))
	}
	free, err := slots.Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(free) != len(res.Free) {
		t.Errorf("Generate returned %d slots; GenerateDetailed returned %d — the wrappers must agree",
			len(free), len(res.Free))
	}
}

func TestGenerateDetailed_noticeGapRespectsTheRoutingMode(t *testing.T) {
	// Collective: a slot is only bookable when BOTH hosts are free. h2 starts at 11:00, so
	// the 09:00-10:30 starts were never bookable by anyone — the notice policy is not what
	// removed them, and reporting them would explain a gap with the wrong cause.
	date := utcDate(2026, 6, 15)
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MinNoticeMinutes:    240, // 09:00 + 4h → nothing before 13:00 is bookable
			RoutingMode:         "collective",
			MaxFutureDays:       30,
		},
		Hosts: []slots.HostAvailability{
			singleHost("h1", time.UTC, monRules("09:00", "17:00")),
			singleHost("h2", time.UTC, monRules("11:00", "17:00")),
		},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: time.UTC,
		Now:      utcTime(2026, 6, 15, 9, 0, 0),
	}
	res, err := slots.GenerateDetailed(req, slots.Extras{NoticeGap: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	for _, s := range res.NoticeGap {
		if s.Start.UTC().Before(utcTime(2026, 6, 15, 11, 0, 0)) {
			t.Errorf("start %v reported as withheld by the notice policy, but h2 does not work then", s.Start)
		}
	}
	// 11:00 through 12:30 were bookable but for the notice.
	if got := len(res.NoticeGap); got != 4 {
		t.Errorf("NoticeGap: got %v; want the four collective starts inside the notice window",
			startTimes(res.NoticeGap))
	}
}

func TestGenerateDetailed_noticeGapIsRenderedInTheBookerTimezone(t *testing.T) {
	// The handler formats these into YYYY-MM-DD day keys the booking surfaces match
	// against, so they must land in the booker's timezone like Free and Taken do.
	ny := mustLoc(t, "America/New_York")
	date := utcDate(2026, 6, 15)
	req := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MinNoticeMinutes:    120,
			RoutingMode:         "fixed",
			MaxFutureDays:       30,
		},
		Hosts:    []slots.HostAvailability{singleHost("h1", time.UTC, monRules("09:00", "17:00"))},
		DateFrom: date,
		DateTo:   date,
		BookerTZ: ny,
		Now:      utcTime(2026, 6, 15, 9, 0, 0),
	}
	res, err := slots.GenerateDetailed(req, slots.Extras{NoticeGap: true})
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if len(res.NoticeGap) == 0 {
		t.Fatal("fixture produced no notice gap")
	}
	for _, s := range res.NoticeGap {
		if s.Start.Location().String() != ny.String() {
			t.Errorf("NoticeGap start %v is in %s; want %s", s.Start, s.Start.Location(), ny)
		}
	}
	// 09:00 UTC on 2026-06-15 is 05:00 in New York, so the day key is still the 15th.
	if got := res.NoticeGap[0].Start.Format("2006-01-02 15:04"); got != "2026-06-15 05:00" {
		t.Errorf("first NoticeGap start: got %q; want %q", got, "2026-06-15 05:00")
	}
}
