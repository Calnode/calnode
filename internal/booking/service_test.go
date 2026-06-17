package booking_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/uid"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return database
}

func seedHost(t *testing.T, database *sql.DB) string {
	t.Helper()
	id := uid.New()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO users (id, email, name, iana_timezone)
		VALUES (?, ?, 'Test Host', 'UTC')`,
		id, id+"@test.com")
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return id
}

func seedEventType(t *testing.T, database *sql.DB, userID string) string {
	t.Helper()
	id := uid.New()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		VALUES (?, ?, ?, '30-min call', 30)`,
		id, userID, id+"-slug")
	if err != nil {
		t.Fatalf("seed event type: %v", err)
	}
	return id
}

// slot returns a UTC time on 2026-06-15 at the given hour and minute.
func slot(h, m int) time.Time {
	return time.Date(2026, 6, 15, h, m, 0, 0, time.UTC)
}

func TestCreate_success(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com", IANATimezone: "America/New_York"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.ID == "" {
		t.Error("Booking.ID is empty")
	}
	if b.Status != "confirmed" {
		t.Errorf("Status = %q; want confirmed", b.Status)
	}
	if !b.StartAt.Equal(slot(9, 0)) {
		t.Errorf("StartAt = %v; want %v", b.StartAt, slot(9, 0))
	}
	if b.HostID != hostID {
		t.Errorf("HostID = %q; want %q", b.HostID, hostID)
	}
}

func TestCreate_doubleBooked_exactStart(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	p := booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	}
	if _, err := svc.Create(context.Background(), p); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(context.Background(), p)
	if err != booking.ErrDoubleBooked {
		t.Errorf("second Create with same start: got %v; want ErrDoubleBooked", err)
	}
}

func TestCreate_doubleBooked_overlap(t *testing.T) {
	// Overlapping but different start times — transaction overlap check catches it.
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	_, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// 09:15-09:45 overlaps with 09:00-09:30.
	_, err = svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 15),
		EndAt:       slot(9, 45),
		Organizer:   booking.Attendee{Name: "Bob", Email: "bob@example.com"},
	})
	if err != booking.ErrDoubleBooked {
		t.Errorf("overlapping Create: got %v; want ErrDoubleBooked", err)
	}
}

func TestCreate_adjacentSlotsAllowed(t *testing.T) {
	// 09:00-09:30 and 09:30-10:00 are adjacent, not overlapping.
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	_, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 30),
		EndAt:       slot(10, 0),
		Organizer:   booking.Attendee{Name: "Bob", Email: "bob@example.com"},
	})
	if err != nil {
		t.Errorf("adjacent Create: got %v; want nil", err)
	}
}

func TestCreate_cancelledDoesNotBlock(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	p := booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	}
	b, err := svc.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Cancel(context.Background(), hostID, b.ID, "cancelled by test"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := svc.Create(context.Background(), p); err != nil {
		t.Errorf("Create after cancel: got %v; want nil", err)
	}
}

func TestCreate_collectiveChecksAllHosts(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	h1 := seedHost(t, database)
	h2 := seedHost(t, database)
	etID := seedEventType(t, database, h1)

	// Occupy h2 at 09:00-09:30.
	_, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{h2},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("seed h2 booking: %v", err)
	}

	// Collective booking for [h1, h2] at the same time should fail — h2 is busy.
	_, err = svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{h1, h2},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Bob", Email: "bob@example.com"},
	})
	if err != booking.ErrDoubleBooked {
		t.Errorf("collective with busy h2: got %v; want ErrDoubleBooked", err)
	}
}

func TestCreate_emptyHostIDs_returnsError(t *testing.T) {
	svc := booking.New(newTestDB(t))
	_, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: "et",
		HostIDs:     nil,
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err == nil {
		t.Error("Create with empty HostIDs: want error, got nil")
	}
}

func TestCreate_roundRobinPriorityPicksFirstFree(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	h1 := seedHost(t, database) // top priority
	h2 := seedHost(t, database)
	etID := seedEventType(t, database, h1)

	// Both free → priority picks the first host in the (priority-ordered) list.
	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID, HostIDs: []string{h1, h2},
		RoutingMode: "round_robin", RRStrategy: "priority",
		StartAt: slot(9, 0), EndAt: slot(9, 30),
		Organizer: booking.Attendee{Name: "A", Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("first booking: %v", err)
	}
	if b.HostID != h1 {
		t.Errorf("priority should pick the top host h1; got %s", b.HostID)
	}

	// Make h1 busy at 10:00; priority then falls to the next free host, h2.
	if _, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID, HostIDs: []string{h1},
		StartAt: slot(10, 0), EndAt: slot(10, 30),
		Organizer: booking.Attendee{Name: "B", Email: "b@example.com"},
	}); err != nil {
		t.Fatalf("seed busy h1: %v", err)
	}
	b2, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID, HostIDs: []string{h1, h2},
		RoutingMode: "round_robin", RRStrategy: "priority",
		StartAt: slot(10, 0), EndAt: slot(10, 30),
		Organizer: booking.Attendee{Name: "C", Email: "c@example.com"},
	})
	if err != nil {
		t.Fatalf("second booking: %v", err)
	}
	if b2.HostID != h2 {
		t.Errorf("priority should fall to free host h2 when h1 is busy; got %s", b2.HostID)
	}
}

func TestCreate_multiHostWritesBookingHosts(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	h1 := seedHost(t, database) // required (primary)
	h2 := seedHost(t, database) // required
	h3 := seedHost(t, database) // optional, free → attends
	h4 := seedHost(t, database) // optional, busy → omitted
	etID := seedEventType(t, database, h1)

	// Make h4 busy at the slot so the optional host is skipped.
	if _, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID, HostIDs: []string{h4},
		StartAt: slot(9, 0), EndAt: slot(9, 30),
		Organizer: booking.Attendee{Name: "Pre", Email: "pre@example.com"},
	}); err != nil {
		t.Fatalf("seed busy h4: %v", err)
	}

	// Group/collective: h1+h2 required (all attend), h3+h4 optional (free ones only).
	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID:   etID,
		HostIDs:       []string{h1, h2},
		RoutingMode:   "collective",
		OptionalHosts: []string{h3, h4},
		StartAt:       slot(9, 0), EndAt: slot(9, 30),
		Organizer: booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("collective Create: %v", err)
	}

	rows, _ := database.Query(`SELECT user_id, is_primary FROM booking_hosts WHERE booking_id = ?`, b.ID)
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var uid string
		var primary int
		rows.Scan(&uid, &primary)
		got[uid] = primary
	}
	if len(got) != 3 {
		t.Fatalf("booking_hosts = %v; want 3 (h1, h2, h3 — h4 busy, omitted)", got)
	}
	if _, ok := got[h4]; ok {
		t.Error("busy optional host h4 should not be a booking host")
	}
	if got[h1] != 1 {
		t.Errorf("h1 should be the primary host; is_primary=%d", got[h1])
	}
	if got[h2] != 0 || got[h3] != 0 {
		t.Error("only the primary host should have is_primary=1")
	}
	if b.HostID != h1 {
		t.Errorf("booking host_id = %s; want primary h1", b.HostID)
	}
}

func TestCancel_success(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Cancel(context.Background(), hostID, b.ID, "changed plans"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, err := svc.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("Get after cancel: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("Status = %q; want cancelled", got.Status)
	}
	if got.CancellationReason != "changed plans" {
		t.Errorf("CancellationReason = %q; want %q", got.CancellationReason, "changed plans")
	}
}

func TestCancel_notFound(t *testing.T) {
	svc := booking.New(newTestDB(t))
	err := svc.Cancel(context.Background(), "any-host", "nonexistent", "")
	if err != booking.ErrNotFound {
		t.Errorf("Cancel nonexistent: got %v; want ErrNotFound", err)
	}
}

func TestCancel_wrongHost(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	h1 := seedHost(t, database)
	h2 := seedHost(t, database)
	etID := seedEventType(t, database, h1)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{h1},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// h2 must not be able to cancel h1's booking.
	err = svc.Cancel(context.Background(), h2, b.ID, "hacked")
	if err != booking.ErrNotFound {
		t.Errorf("Cancel with wrong host: got %v; want ErrNotFound", err)
	}
}

func TestCancel_alreadyCancelled(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{hostID},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Cancel(context.Background(), hostID, b.ID, "first"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	err = svc.Cancel(context.Background(), hostID, b.ID, "second")
	if err != booking.ErrAlreadyCancelled {
		t.Errorf("second Cancel: got %v; want ErrAlreadyCancelled", err)
	}
}

func TestGet_success(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hostID := seedHost(t, database)
	etID := seedEventType(t, database, hostID)

	created, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID:   etID,
		HostIDs:       []string{hostID},
		StartAt:       slot(9, 0),
		EndAt:         slot(9, 30),
		LocationValue: "https://meet.example.com/abc",
		Organizer:     booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q; want %q", got.ID, created.ID)
	}
	if got.LocationValue != "https://meet.example.com/abc" {
		t.Errorf("LocationValue = %q; want %q", got.LocationValue, "https://meet.example.com/abc")
	}
	if !got.StartAt.Equal(slot(9, 0)) {
		t.Errorf("StartAt = %v; want %v", got.StartAt, slot(9, 0))
	}
}

func TestGet_notFound(t *testing.T) {
	svc := booking.New(newTestDB(t))
	_, err := svc.Get(context.Background(), "nonexistent")
	if err != booking.ErrNotFound {
		t.Errorf("Get nonexistent: got %v; want ErrNotFound", err)
	}
}

func TestListByHost(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	h1 := seedHost(t, database)
	h2 := seedHost(t, database)
	etID := seedEventType(t, database, h1)

	create := func(hostID string, start, end time.Time) *booking.Booking {
		b, err := svc.Create(context.Background(), booking.CreateParams{
			EventTypeID: etID,
			HostIDs:     []string{hostID},
			StartAt:     start,
			EndAt:       end,
			Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return b
	}

	b1 := create(h1, slot(9, 0), slot(9, 30))
	b2 := create(h1, slot(10, 0), slot(10, 30))
	create(h2, slot(9, 0), slot(9, 30)) // different host — must not appear
	cancelled := create(h1, slot(11, 0), slot(11, 30))
	svc.Cancel(context.Background(), h1, cancelled.ID, "test") //nolint:errcheck

	bookings, err := svc.ListByHost(context.Background(), h1)
	if err != nil {
		t.Fatalf("ListByHost: %v", err)
	}
	if len(bookings) != 2 {
		t.Fatalf("ListByHost returned %d bookings; want 2", len(bookings))
	}
	if bookings[0].ID != b1.ID {
		t.Errorf("[0].ID = %q; want %q", bookings[0].ID, b1.ID)
	}
	if bookings[1].ID != b2.ID {
		t.Errorf("[1].ID = %q; want %q", bookings[1].ID, b2.ID)
	}
}

func TestCreate_collective_success(t *testing.T) {
	// Both hosts free — collective booking should succeed and record the primary host.
	database := newTestDB(t)
	svc := booking.New(database)
	h1 := seedHost(t, database)
	h2 := seedHost(t, database)
	etID := seedEventType(t, database, h1)

	b, err := svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{h1, h2},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Alice", Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("collective Create: %v", err)
	}
	if b.Status != "confirmed" {
		t.Errorf("Status = %q; want confirmed", b.Status)
	}
	if b.HostID != h1 {
		t.Errorf("HostID = %q; want primary host %q", b.HostID, h1)
	}

	// Verify persisted via Get.
	got, err := svc.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("Get after collective Create: %v", err)
	}
	if got.HostID != h1 {
		t.Errorf("persisted HostID = %q; want %q", got.HostID, h1)
	}

	// h1 is now busy — a solo booking for h1 at the same time must fail.
	_, err = svc.Create(context.Background(), booking.CreateParams{
		EventTypeID: etID,
		HostIDs:     []string{h1},
		StartAt:     slot(9, 0),
		EndAt:       slot(9, 30),
		Organizer:   booking.Attendee{Name: "Bob", Email: "bob@example.com"},
	})
	if err != booking.ErrDoubleBooked {
		t.Errorf("solo booking after collective: got %v; want ErrDoubleBooked", err)
	}
}

func TestListByHost_empty(t *testing.T) {
	svc := booking.New(newTestDB(t))
	bookings, err := svc.ListByHost(context.Background(), "nonexistent-host")
	if err != nil {
		t.Fatalf("ListByHost empty: %v", err)
	}
	if len(bookings) != 0 {
		t.Errorf("ListByHost empty: got %d bookings; want 0", len(bookings))
	}
}
