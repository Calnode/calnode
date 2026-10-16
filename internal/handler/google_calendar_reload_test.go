package handler_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

type reloadCalendar struct {
	calendar.Provider
	name string
}

func (p *reloadCalendar) Name() string { return p.name }

// Exercise the real settings handler followed by the real reminder worker.
// Neither saving nor clearing Google credentials may remove another provider.
func TestGoogleSettingsReloadPreservesOtherReminders(t *testing.T) {
	for _, provider := range []string{"microsoft", "caldav"} {
		for _, action := range []string{"save", "clear"} {
			t.Run(provider+"/"+action, func(t *testing.T) {
				h, database, key, hostID := setupWorkspaceWithDB(t)
				h.SetEncKey(testGCalKeyHex)
				h.SetBaseURL("http://localhost:3000")
				_, etID := seedEventTypeHTTP(t, h, key)
				original := calendar.NewService(database)
				original.Register(&reloadCalendar{name: "google"})
				original.Register(&reloadCalendar{name: "microsoft"})
				original.Register(&reloadCalendar{name: "caldav"})
				h.SetCalendar(original)
				body := `{"client_id":"new-id","client_secret":"new-secret"}`
				if action == "clear" {
					body = `{"client_id":""}`
				}
				rec := patchGoogleSettings(t, h, body, key)
				if rec.Code != http.StatusOK {
					t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
				}
				req := authReq(http.MethodGet, "/v1/calendar/status", "", key)
				statusRec := httptest.NewRecorder()
				h.RequireAuth(h.CalendarStatus)(statusRec, req)
				if statusRec.Code != http.StatusOK {
					t.Fatalf("calendar status: %d %s", statusRec.Code, statusRec.Body.String())
				}
				var status struct {
					Providers []string `json:"providers"`
				}
				if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
					t.Fatal(err)
				}
				wantProviders := []string{"caldav", "microsoft"}
				if action == "save" {
					wantProviders = []string{"caldav", "google", "microsoft"}
				}
				if !slices.Equal(status.Providers, wantProviders) {
					t.Fatalf("providers=%v, want %v", status.Providers, wantProviders)
				}
				if original.Provider("google") == nil || len(original.ProviderNames()) != 3 {
					t.Fatal("reload mutated in-flight service")
				}
				start := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
				calendarCheckExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('reload-booking',?,?,?,?,'confirmed')`, etID, hostID, start, start)
				calendarCheckExec(t, database, `INSERT INTO booking_hosts (booking_id,user_id,external_event_id,external_calendar_id,external_provider) VALUES ('reload-booking',?,'original-event','original-calendar',?)`, hostID, provider)
				calendarCheckExec(t, database, `INSERT INTO booking_attendees (id,booking_id,name,email,iana_timezone,is_organizer) VALUES ('reload-att','reload-booking','Test','test@example.com','UTC',1)`)
				calendarCheckExec(t, database, `INSERT INTO jobs (id,type,payload,run_at,status,attempts,max_attempts) VALUES ('reload-job','reminder.send','{"booking_id":"reload-booking"}',?,'pending',0,3)`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339))
				whs, err := webhook.New(database, strings.Repeat("01", 32))
				if err != nil {
					t.Fatal(err)
				}
				m := &reminderMailer{}
				w := worker.New(database, whs, slog.Default(), worker.WithMailer(m), worker.WithReminderCheck(h.ReminderAllowed))
				w.Poll(context.Background())
				if m.sent != 1 {
					t.Fatalf("sent=%d, want 1 after Google %s", m.sent, action)
				}
				var jobStatus string
				if err := database.QueryRow(`SELECT status FROM jobs WHERE id='reload-job'`).Scan(&jobStatus); err != nil {
					t.Fatal(err)
				}
				if jobStatus != "done" {
					t.Fatalf("job=%s, want done", jobStatus)
				}
				// Missing Google credentials still cannot authorize an unchecked Google reminder.
				calendarCheckExec(t, database, `UPDATE booking_hosts SET external_provider='google' WHERE booking_id='reload-booking'`)
				allowed, err := h.ReminderAllowed(context.Background(), "reload-booking")
				if allowed || err == nil {
					t.Fatalf("unchecked Google reminder allowed=%v err=%v", allowed, err)
				}
			})
		}
	}
}
