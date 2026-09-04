package handler_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// The minimum-notice policy hides the nearest starts and leaves nothing behind to explain
// them (#20). GET /slots therefore reports the policy and the days it actually cost
// something, which is what lets the three booking surfaces say so without re-deriving it.
//
// These tests run against the real clock, because computeSlots calls time.Now itself.
// Every assertion is therefore phrased relative to now rather than against a fixture
// date — a test that only holds between 09:00 and 17:00 would be worse than none.

type slotsPayload = slotsBody

// seedNoticeEventType inserts a bookable event type whose single host is available around
// the clock every day, so the only thing that can remove a start is the notice policy.
func seedNoticeEventType(t *testing.T, database *sql.DB, ownerID, slug string, minNoticeMinutes int) {
	t.Helper()
	etID := uid.New()
	if _, err := database.Exec(`
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes, slot_interval_minutes,
		                         min_notice_minutes, max_future_days, is_active, is_public)
		VALUES (?, ?, ?, 'Intro Call', 30, 30, ?, 0, 1, 1)`,
		etID, ownerID, slug, minNoticeMinutes); err != nil {
		t.Fatalf("seed event type: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
		VALUES (?, ?, ?, 'required', 0)`, uid.New(), etID, ownerID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	for day := 0; day < 7; day++ {
		if _, err := database.Exec(`
			INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time)
			VALUES (?, ?, ?, '00:00', '23:59')`, uid.New(), ownerID, day); err != nil {
			t.Fatalf("seed availability day %d: %v", day, err)
		}
	}
}

func TestGetSlots_reportsTheDaysMinimumNoticeEmptied(t *testing.T) {
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	seedNoticeEventType(t, database, ownerID, "notice-call", 24*60)

	now := time.Now().UTC()
	from := now.Format("2006-01-02")
	to := now.AddDate(0, 0, 2).Format("2006-01-02")
	got := getSlots(t, h, "notice-call", "?from="+from+"&to="+to+"&tz=UTC")

	if got.MinNotice == nil {
		t.Fatal("min_notice is absent for an event type that sets one")
	}
	if got.MinNotice.Minutes != 24*60 {
		t.Errorf("min_notice.minutes: got %d; want %d", got.MinNotice.Minutes, 24*60)
	}
	if len(got.MinNotice.Dates) == 0 {
		t.Fatal("min_notice.dates is empty, but a 24-hour notice on an all-hours calendar must have hidden today's remaining starts")
	}
	// The notice window is 24 hours from now, so it can only touch today and tomorrow.
	allowed := map[string]bool{
		from: true,
		now.AddDate(0, 0, 1).Format("2006-01-02"): true,
	}
	for _, d := range got.MinNotice.Dates {
		if !allowed[d] {
			t.Errorf("min_notice.dates contains %q, which is outside the 24-hour notice window (today/tomorrow)", d)
		}
	}
	// And the policy really is in force: nothing bookable inside the window.
	cutoff := now.Add(24 * time.Hour)
	if len(got.Slots) == 0 {
		t.Fatal("no slots at all; the fixture is meant to leave later days bookable")
	}
	for _, s := range got.Slots {
		start, err := time.Parse(time.RFC3339, s.Start)
		if err != nil {
			t.Fatalf("parse slot start %q: %v", s.Start, err)
		}
		if start.Before(cutoff) {
			t.Errorf("slot at %s is inside the 24-hour notice window", s.Start)
		}
	}
}

func TestGetSlots_omitsMinNoticeWhenThereIsNoPolicy(t *testing.T) {
	// Absent rather than zeroed, so a client can tell "no such policy" from "the policy
	// cost you nothing here" — the same distinction `taken` draws.
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	seedNoticeEventType(t, database, ownerID, "open-call", 0)

	now := time.Now().UTC()
	got := getSlots(t, h, "open-call",
		"?from="+now.Format("2006-01-02")+"&to="+now.AddDate(0, 0, 1).Format("2006-01-02")+"&tz=UTC")
	if got.MinNotice != nil {
		t.Errorf("min_notice present for an event type with no minimum notice: %+v", *got.MinNotice)
	}
}

func TestGetSlots_minNoticeDatesEmptyWhenThePolicyCostThisRangeNothing(t *testing.T) {
	// A one-minute notice, asked about days that start tomorrow: the policy exists and is
	// reported, but it took nothing away in this window, so nothing should be explained.
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	seedNoticeEventType(t, database, ownerID, "tiny-notice", 1)

	now := time.Now().UTC()
	from := now.AddDate(0, 0, 1).Format("2006-01-02")
	to := now.AddDate(0, 0, 3).Format("2006-01-02")
	got := getSlots(t, h, "tiny-notice", "?from="+from+"&to="+to+"&tz=UTC")

	if got.MinNotice == nil {
		t.Fatal("min_notice is absent for an event type that sets one")
	}
	if len(got.MinNotice.Dates) != 0 {
		t.Errorf("min_notice.dates: got %v; want empty (the window starts tomorrow)", got.MinNotice.Dates)
	}
}

func TestGetSlots_minNoticeDatesUseTheRequestedTimezone(t *testing.T) {
	// The surfaces group slots by day in the tz they asked for, so these keys have to be
	// in that tz too or the explanation lands on the wrong day. Auckland is up to 13 hours
	// ahead of UTC, so its "today" differs from UTC's for half the clock.
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	seedNoticeEventType(t, database, ownerID, "tz-call", 24*60)

	akl, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skipf("Pacific/Auckland unavailable: %v", err)
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -1).Format("2006-01-02")
	to := now.AddDate(0, 0, 2).Format("2006-01-02")
	got := getSlots(t, h, "tz-call", "?from="+from+"&to="+to+"&tz=Pacific%2FAuckland")

	if got.MinNotice == nil || len(got.MinNotice.Dates) == 0 {
		t.Fatal("expected min_notice dates for a 24-hour notice on an all-hours calendar")
	}
	nowAKL := time.Now().In(akl)
	allowed := map[string]bool{
		nowAKL.Format("2006-01-02"):                  true,
		nowAKL.AddDate(0, 0, 1).Format("2006-01-02"): true,
	}
	for _, d := range got.MinNotice.Dates {
		if !allowed[d] {
			t.Errorf("min_notice.dates contains %q; want Auckland-local today/tomorrow (%v)", d, allowed)
		}
	}
}
