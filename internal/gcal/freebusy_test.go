package gcal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// mockCalendarServer returns an httptest.Server that serves a freeBusy response
// and records the Authorization header it receives.
func mockFreeBusyServer(t *testing.T, calID string, busyTimes [][2]string) (*httptest.Server, *string) {
	t.Helper()
	gotAuth := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")

		type period struct {
			Start string `json:"start"`
			End   string `json:"end"`
		}
		type calEntry struct {
			Busy []period `json:"busy"`
		}
		resp := struct {
			Calendars map[string]calEntry `json:"calendars"`
		}{
			Calendars: map[string]calEntry{
				calID: {
					Busy: func() []period {
						out := make([]period, len(busyTimes))
						for i, bt := range busyTimes {
							out[i] = period{Start: bt[0], End: bt[1]}
						}
						return out
					}(),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, gotAuth
}

func saveAndConnectClient(t *testing.T, c *Client, userID, calID, accessToken string) {
	t.Helper()
	seedUser(t, c.db, userID)
	tok := &oauth2.Token{
		AccessToken: accessToken,
		Expiry:      time.Now().Add(time.Hour), // valid; no refresh needed
	}
	if err := c.saveToken(context.Background(), userID, calID, "", tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FreeBusy
// ---------------------------------------------------------------------------

func TestFreeBusy_returnsIntervals(t *testing.T) {
	start1 := "2026-06-15T09:00:00Z"
	end1 := "2026-06-15T10:00:00Z"
	start2 := "2026-06-15T14:00:00Z"
	end2 := "2026-06-15T15:30:00Z"

	srv, _ := mockFreeBusyServer(t, "primary", [][2]string{{start1, end1}, {start2, end2}})

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveAndConnectClient(t, c, "user-1", "primary", "test-access-token")

	from := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)

	intervals, err := c.FreeBusy(context.Background(), "user-1", from, to)
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(intervals) != 2 {
		t.Fatalf("got %d intervals; want 2", len(intervals))
	}
	if intervals[0].Start.Format(time.RFC3339) != start1 {
		t.Errorf("interval[0].Start = %v; want %v", intervals[0].Start.Format(time.RFC3339), start1)
	}
	if intervals[1].End.Format(time.RFC3339) != end2 {
		t.Errorf("interval[1].End = %v; want %v", intervals[1].End.Format(time.RFC3339), end2)
	}
}

func TestFreeBusy_sendsAuthorizationHeader(t *testing.T) {
	srv, gotAuth := mockFreeBusyServer(t, "primary", nil)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveAndConnectClient(t, c, "user-1", "primary", "my-access-token")

	from := time.Now().UTC()
	c.FreeBusy(context.Background(), "user-1", from, from.Add(24*time.Hour)) //nolint:errcheck

	if !strings.HasPrefix(*gotAuth, "Bearer ") {
		t.Errorf("Authorization header = %q; want Bearer ...", *gotAuth)
	}
}

func TestFreeBusy_emptyBusyList(t *testing.T) {
	srv, _ := mockFreeBusyServer(t, "primary", nil)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveAndConnectClient(t, c, "user-1", "primary", "tok")

	from := time.Now().UTC()
	intervals, err := c.FreeBusy(context.Background(), "user-1", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(intervals) != 0 {
		t.Errorf("got %d intervals; want 0", len(intervals))
	}
}

func TestFreeBusy_notConnected_returnsNil(t *testing.T) {
	c := newTestClient(t)
	intervals, err := c.FreeBusy(context.Background(), "user-no-connection", time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if intervals != nil {
		t.Errorf("got %v; want nil", intervals)
	}
}

func TestFreeBusy_nonOK_failsOpen(t *testing.T) {
	// A connection returning non-200 is skipped (fail-open), not surfaced as an error —
	// a flaky calendar must never block availability or a booking.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveAndConnectClient(t, c, "user-1", "primary", "bad-token")

	got, err := c.FreeBusy(context.Background(), "user-1", time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Errorf("fail-open: expected nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no busy intervals from a failed connection, got %d", len(got))
	}
}

func TestFreeBusy_onlyCheckConflictsConnections(t *testing.T) {
	// Store a connection with check_conflicts = 0 — FreeBusy should return nil.
	c := newTestClient(t)
	seedUser(t, c.db, "user-1")
	tok := &oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}
	if err := c.saveToken(context.Background(), "user-1", "primary", "", tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	// Flip check_conflicts to 0.
	c.db.ExecContext(context.Background(), //nolint:errcheck
		`UPDATE calendar_connections SET check_conflicts = 0 WHERE user_id = ?`, "user-1")

	intervals, err := c.FreeBusy(context.Background(), "user-1", time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if intervals != nil {
		t.Errorf("got intervals from check_conflicts=0 connection; want nil")
	}
}
