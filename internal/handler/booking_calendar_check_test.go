package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/slots"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type conflictCalendar struct {
	calendar.Provider
	name  string
	busy  map[string][]slots.Interval
	err   error
	calls int
}

func (p *conflictCalendar) Name() string { return p.name }
func (p *conflictCalendar) FreeBusy(_ context.Context, id string, from, to time.Time) ([]slots.Interval, error) {
	p.calls++
	var busy []slots.Interval
	for _, iv := range p.busy[id] {
		if iv.Start.Before(to) && iv.End.After(from) {
			busy = append(busy, iv)
		}
	}
	return busy, p.err
}

func calendarCheckRequest(h *handler.Handler, slug string, start time.Time) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Test","email":"test@example.com"}`, slug, start.Format(time.RFC3339))))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	return rec
}

func calendarCheckExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestBookingCalendarHostSelection(t *testing.T) {
	start := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, mode    string
		roles         []string
		busy          []int
		providerError bool
		wantStatus    int
		wantHosts     []int
	}{
		{"required_busy", "fixed", []string{"required"}, []int{0}, false, 409, nil},
		{"provider_unavailable", "fixed", []string{"required"}, nil, true, 503, nil},
		{"rotation_filters_busy", "round_robin", []string{"rotation", "rotation"}, []int{0}, false, 201, []int{1}},
		{"all_rotation_busy", "round_robin", []string{"rotation", "rotation"}, []int{0, 1}, false, 409, nil},
		{"round_robin_required_busy", "round_robin", []string{"required", "rotation"}, []int{0}, false, 409, nil},
		{"round_robin_required_attends", "round_robin", []string{"required", "rotation"}, nil, false, 201, []int{0, 1}},
		{"collective_required_busy", "collective", []string{"required", "required"}, []int{1}, false, 409, nil},
		{"optional_busy_dropped", "fixed", []string{"required", "optional"}, []int{1}, false, 201, []int{0}},
		{"optional_free_attends", "fixed", []string{"required", "optional"}, nil, false, 201, []int{0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, etID := seedEventTypeHTTP(t, h, key)
			calendarCheckExec(t, database, `UPDATE event_types SET routing_mode = ? WHERE id = ?`, tc.mode, etID)
			calendarCheckExec(t, database, `DELETE FROM event_type_hosts WHERE event_type_id = ?`, etID)
			hosts := []string{owner}
			for i, role := range tc.roles {
				if i > 0 {
					id := fmt.Sprintf("host-%d", i)
					calendarCheckExec(t, database, `INSERT INTO users (id,email,name,iana_timezone) VALUES (?,?,?,'UTC')`, id, id+"@example.com", id)
					seedFullAvailabilityDB(t, database, id)
					hosts = append(hosts, id)
				}
				calendarCheckExec(t, database, `INSERT INTO event_type_hosts (id,event_type_id,user_id,role,priority) VALUES (?,?,?,?,?)`, fmt.Sprintf("seat-%d", i), etID, hosts[i], role, i)
			}
			provider := &conflictCalendar{name: "google", busy: map[string][]slots.Interval{}}
			for _, i := range tc.busy {
				provider.busy[hosts[i]] = []slots.Interval{{Start: start, End: start.Add(time.Hour)}}
			}
			if tc.providerError {
				provider.err = errors.New("private provider diagnostic")
			}
			svc := calendar.NewService(database)
			svc.Register(provider)
			if tc.providerError {
				svc.Register(&conflictCalendar{name: "other"})
			}
			h.SetCalendar(svc)
			rec := calendarCheckRequest(h, slug, start)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if provider.calls == 0 {
				t.Fatal("booking did not check the provider")
			}
			if tc.providerError && (!strings.Contains(rec.Body.String(), "try again") || strings.Contains(rec.Body.String(), "private provider")) {
				t.Fatalf("provider error not safely mapped: %s", rec.Body)
			}
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if tc.wantStatus != 201 {
				if count != 0 {
					t.Fatalf("created %d bookings after failed check", count)
				}
				return
			}
			if count != 1 {
				t.Fatalf("created %d bookings", count)
			}
			rows, err := database.Query(`SELECT user_id FROM booking_hosts ORDER BY user_id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				got = append(got, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, i := range tc.wantHosts {
				want = append(want, hosts[i])
			}
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("hosts = %v, want %v", got, want)
			}
		})
	}
}

func TestBookingCalendarBuffersMatchSlots(t *testing.T) {
	start := time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                      string
		before, after, busyOffset int
		wantFree                  bool
	}{
		{"before_blocks_later_event", 15, 0, 30, false},
		{"after_blocks_earlier_event", 0, 30, -45, false},
		{"before_allows_earlier_event", 15, 0, -30, true},
		{"after_allows_later_event", 0, 30, 45, true},
		{"before_touching_boundary", 15, 0, 45, true},
		{"after_touching_boundary", 0, 30, -60, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, etID := seedEventTypeHTTP(t, h, key)
			calendarCheckExec(t, database, `UPDATE event_types SET buffer_before_minutes = ?, buffer_after_minutes = ? WHERE id = ?`, tc.before, tc.after, etID)
			busyStart := start.Add(time.Duration(tc.busyOffset) * time.Minute)
			busy := []slots.Interval{{Start: busyStart, End: busyStart.Add(30 * time.Minute)}}
			generated, err := slots.Generate(slots.Request{
				Event:    slots.EventConfig{DurationMinutes: 30, SlotIntervalMinutes: 30, BufferBeforeMinutes: tc.before, BufferAfterMinutes: tc.after, RoutingMode: "fixed"},
				Hosts:    []slots.HostAvailability{{HostID: owner, Location: time.UTC, Rules: []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "00:00", EndTime: "23:59"}}, Busy: busy}},
				DateFrom: start, DateTo: start, BookerTZ: time.UTC, Now: start.Add(-24 * time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			offered := slices.ContainsFunc(generated, func(s slots.Slot) bool { return s.Start.Equal(start) })
			if offered != tc.wantFree {
				t.Fatalf("slot offered = %v, want %v", offered, tc.wantFree)
			}
			svc := calendar.NewService(database)
			svc.Register(&conflictCalendar{name: "google", busy: map[string][]slots.Interval{owner: busy}})
			h.SetCalendar(svc)
			rec := calendarCheckRequest(h, slug, start)
			want := http.StatusConflict
			if offered {
				want = http.StatusCreated
			}
			if rec.Code != want {
				t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body)
			}
		})
	}
}

func TestBookingCalendarCancelledOwnEvent(t *testing.T) {
	start := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	for _, otherEvent := range []bool{false, true} {
		t.Run(fmt.Sprintf("otherEvent=%v", otherEvent), func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, etID := seedEventTypeHTTP(t, h, key)
			calendarCheckExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('cancelled',?,?,?,?,'cancelled')`, etID, owner, start.Add(-time.Hour).Format(time.RFC3339), start.Add(time.Hour).Format(time.RFC3339))
			calendarCheckExec(t, database, `INSERT INTO booking_hosts (booking_id,user_id,external_event_id) VALUES ('cancelled',?,'old-event')`, owner)
			busyEnd := start.Add(time.Hour)
			bookingStart := start
			if otherEvent {
				busyEnd = busyEnd.Add(30 * time.Minute)
				bookingStart = start.Add(time.Hour)
			}
			svc := calendar.NewService(database)
			svc.Register(&conflictCalendar{name: "google", busy: map[string][]slots.Interval{owner: {{Start: start.Add(-time.Hour), End: busyEnd}}}})
			h.SetCalendar(svc)
			rec := calendarCheckRequest(h, slug, bookingStart)
			want := http.StatusCreated
			if otherEvent {
				want = http.StatusConflict
			}
			if rec.Code != want {
				t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body)
			}
		})
	}
}

func TestMCPBookingCalendarErrors(t *testing.T) {
	start := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	for _, outage := range []bool{false, true} {
		t.Run(fmt.Sprintf("outage=%v", outage), func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, _ := seedEventTypeHTTP(t, h, key)
			provider := &conflictCalendar{name: "google", busy: map[string][]slots.Interval{owner: {{Start: start, End: start.Add(time.Hour)}}}}
			want := "this slot is no longer available"
			if outage {
				provider.err = errors.New("private provider diagnostic")
				want = "calendar availability could not be checked; please try again shortly"
			}
			svc := calendar.NewService(database)
			svc.Register(provider)
			h.SetCalendar(svc)
			client := connectMCP(t, h)
			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_booking", Arguments: map[string]any{
				"event_type_id": slug, "slot_start": start.Format(time.RFC3339), "attendee_name": "Test", "attendee_email": "test@example.com",
			}})
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(result.Content)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || !strings.Contains(string(body), want) || strings.Contains(string(body), "private provider") {
				t.Fatalf("MCP error: %s", body)
			}
			if provider.calls == 0 {
				t.Fatal("MCP booking did not check the provider")
			}
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("created %d bookings", count)
			}
		})
	}
}
