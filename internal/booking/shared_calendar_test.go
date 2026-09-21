package booking_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/calnode/calnode/internal/booking"
)

func TestSharedCalendarAtomicConflict(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	hosts := []string{seedHost(t, database), seedHost(t, database)}
	events := []string{seedEventType(t, database, hosts[0]), seedEventType(t, database, hosts[1])}
	for _, host := range hosts {
		_, err := database.Exec(`INSERT INTO connection_calendars (id,user_id,provider,account_email,calendar_id,check_conflicts,is_destination) VALUES (?,?,'google','shared@example.com','primary',1,1)`, host, host)
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	ids := make(chan string, 2)
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			b, err := svc.Create(context.Background(), booking.CreateParams{EventTypeID: events[i], HostIDs: []string{hosts[i]}, StartAt: slot(9, 0), EndAt: slot(9, 30), Organizer: booking.Attendee{Name: "Test", Email: "test@example.com", IANATimezone: "UTC"}})
			results <- err
			if b != nil {
				ids <- b.ID
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, booking.ErrDoubleBooked) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	if err := svc.CancelByID(context.Background(), <-ids, "test cleanup"); err != nil {
		t.Fatal(err)
	}
	for i := range hosts {
		b, err := svc.Create(context.Background(), booking.CreateParams{EventTypeID: events[i], HostIDs: []string{hosts[i]}, StartAt: slot(9, 0), EndAt: slot(9, 30), Organizer: booking.Attendee{Name: "Test", Email: "test@example.com", IANATimezone: "UTC"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.CancelByID(context.Background(), b.ID, "test cleanup"); err != nil {
			t.Fatal(err)
		}
	}
}
