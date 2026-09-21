package calendar

import (
	"context"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/slots"
)

// recordProvider is a stub Provider that logs which of its write ops ran, so
// routing tests can assert WHERE an event went rather than merely that it went.
type recordProvider struct {
	name      string
	recognize func(string) bool
	log       *[]string
}

func (p *recordProvider) Name() string                                           { return p.name }
func (p *recordProvider) InvitesGuests() bool                                    { return false }
func (p *recordProvider) AuthURL(string) string                                  { return "" }
func (p *recordProvider) EncryptState(string) (string, error)                    { return "", nil }
func (p *recordProvider) DecryptState(string) (string, error)                    { return "", nil }
func (p *recordProvider) Exchange(context.Context, string, string, string) error { return nil }
func (p *recordProvider) Connected(context.Context, string) (bool, error)        { return true, nil }
func (p *recordProvider) Disconnect(context.Context, string) error               { return nil }
func (p *recordProvider) HasDestination(context.Context, string) (bool, error)   { return true, nil }
func (p *recordProvider) ListCalendars(context.Context, string, string) ([]CalendarInfo, error) {
	return nil, nil
}
func (p *recordProvider) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, nil
}
func (p *recordProvider) CreateEvent(context.Context, string, CreateEventParams) (string, string, string, error) {
	return "", "", "", nil
}
func (p *recordProvider) UpdateEvent(context.Context, string, string, string, time.Time, time.Time) error {
	*p.log = append(*p.log, p.name+":update")
	return nil
}
func (p *recordProvider) CancelEvent(context.Context, string, string, string) error {
	*p.log = append(*p.log, p.name+":cancel")
	return nil
}
func (p *recordProvider) RecognizesEvent(id string) bool {
	return p.recognize != nil && p.recognize(id)
}

// TestProviderForEvent_prefersStampedProvider is the fix for issue #58: an event
// written by Google must go back to Google even after the destination moved to
// Microsoft. Empty stamps keep the old behaviour (recognition, then destination);
// unknown stamps fall through instead of stranding the event.
func TestProviderForEvent_prefersStampedProvider(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := database.ExecContext(ctx,
		`INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		 VALUES ('host', 'host@x.test', 'H', 'UTC', 0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, pc := range []struct {
		provider string
		dest     int
	}{
		{"google", 0},
		{"microsoft", 1}, // current destination
	} {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO calendar_connections
			   (id, user_id, provider, access_token_enc, calendar_id,
			    check_conflicts, is_destination, created_at)
			 VALUES (?, 'host', ?, 'e', 'primary', 1, ?, '2026-01-01T00:00:00Z')`,
			"host-"+pc.provider, pc.provider, pc.dest); err != nil {
			t.Fatal(err)
		}
	}

	var log []string
	isURL := func(id string) bool { return len(id) > 8 && id[:8] == "https://" }
	svc := NewService(database)
	svc.Register(&recordProvider{name: "google", log: &log})
	svc.Register(&recordProvider{name: "microsoft", log: &log})
	svc.Register(&recordProvider{name: "caldav", recognize: isURL, log: &log})

	start := time.Now()
	cases := []struct {
		name, eventID, stamp string
		want                 string
	}{
		{"stamped google beats microsoft destination", "opaque-google-id", "google", "google"},
		{"empty stamp falls back to destination", "opaque-google-id", "", "microsoft"},
		{"unknown stamp falls back to destination", "opaque-google-id", "nope", "microsoft"},
		{"caldav URL recognized without stamp", "https://cal.example/x/evt.ics", "", "caldav"},
		{"stamp beats recognition", "https://cal.example/x/evt.ics", "microsoft", "microsoft"},
	}
	for _, tc := range cases {
		log = nil
		if err := svc.UpdateEvent(ctx, "host", "cal", tc.eventID, tc.stamp, start, start.Add(time.Hour)); err != nil {
			t.Fatalf("%s: UpdateEvent: %v", tc.name, err)
		}
		if len(log) != 1 || log[0] != tc.want+":update" {
			t.Errorf("%s: routed to %v, want %s", tc.name, log, tc.want)
		}
		log = nil
		if err := svc.CancelEvent(ctx, "host", "cal", tc.eventID, tc.stamp); err != nil {
			t.Fatalf("%s: CancelEvent: %v", tc.name, err)
		}
		if len(log) != 1 || log[0] != tc.want+":cancel" {
			t.Errorf("%s: routed to %v, want %s", tc.name, log, tc.want)
		}
	}
}
