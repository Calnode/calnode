package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
	"github.com/calnode/calnode/internal/webhook"
)

type invitationCalendar struct {
	telephoneCalendar
	moved     chan slots.Interval
	cancelled chan struct{}
}

func (p invitationCalendar) UpdateEvent(_ context.Context, _, _, _ string, start, end time.Time) error {
	p.moved <- slots.Interval{Start: start, End: end}
	return nil
}
func (p invitationCalendar) CancelEvent(context.Context, string, string, string) error {
	p.cancelled <- struct{}{}
	return nil
}

func TestInvitationWorkflowSnapshotsCalendarManagementAndCorrelation(t *testing.T) {
	h, db, key, owner, slug, etID, day := invitationFixture(t)
	calendarCheckExec(t, db, `INSERT INTO users (id,email,name,iana_timezone) VALUES ('selected','selected@example.com','Selected Host','UTC')`)
	seedInvitationHours(t, db, "selected", day, "09:00", "12:00")
	mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"rotation"},{"user_id":"selected","role":"rotation"}]}`, owner)), 200, "hosts")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"routing_mode":"round_robin","min_duration_minutes":15,"max_duration_minutes":120,"duration_increment_minutes":15,"is_public":false}`), 200, "template")
	calendarCheckExec(t, db, `INSERT INTO event_type_questions (id,event_type_id,label,type,required) VALUES ('intake',?,'Reason','text',1)`, etID)
	body := invitationBody(slug)
	body["duration_minutes"] = 60
	body["host_id"] = "selected"
	body["available_from"] = day.Add(10 * time.Hour).Format(time.RFC3339)
	body["available_until"] = day.Add(12 * time.Hour).Format(time.RFC3339)
	body["external"] = map[string]string{"system": "zammad", "reference": "ticket-123", "url": "https://tickets.example/123"}
	svc, err := webhook.New(db, "")
	if err != nil {
		t.Fatal(err)
	}
	h.SetWebhookSvc(svc)
	_, _, err = svc.Create(context.Background(), owner, "https://receiver.example/hook", []string{"booking.created", "booking.rescheduled", "booking.cancelled", "scheduling_invitation.created", "scheduling_invitation.booked"})
	if err != nil {
		t.Fatal(err)
	}
	rec, inv := createInvitation(t, h, key, body)
	mustStatus(t, rec, 201, "create")
	p := invitationCalendar{telephoneCalendar: telephoneCalendar{events: make(chan calendar.CreateEventParams, 2)}, moved: make(chan slots.Interval, 2), cancelled: make(chan struct{}, 2)}
	cal := calendar.NewService(db)
	cal.Register(p)
	h.SetCalendar(cal)
	calendarCheckExec(t, db, `INSERT INTO calendar_connections (id,user_id,provider,access_token_enc,calendar_id,is_destination) VALUES ('selected-cal','selected','google','test','primary',1)`)
	// The recipient, duration, host, window and location are already authorized.
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"duration_minutes":90,"location_type":"in_person","location_value":"Changed room"}`), 200, "change template")
	mustStatus(t, putHosts(t, h, slug, key, fmt.Sprintf(`{"hosts":[{"user_id":%q,"role":"required"}]}`, owner)), 200, "replace template hosts")
	mustStatus(t, invitationSubmit(h, inv.Token, day.Add(10*time.Hour)), 400, "required intake")
	submit := fmt.Sprintf(`{"start_at":%q,"timezone":"UTC","answers":[{"question_id":"intake","value":"Support"}]}`, day.Add(10*time.Hour).Format(time.RFC3339))
	rec = invitationPublic(h, h.CreateInvitationBooking, "POST", inv.Token, "/book", submit)
	response := mustJSON(t, rec, 201, "book")
	id := mustString(t, response, "id", "book")
	waitInvitationConfirmation(t, db, id)
	select {
	case event := <-p.events:
		if event.Start != day.Add(10*time.Hour) || event.End != day.Add(11*time.Hour) || event.OrganizerEmail != "customer@example.com" {
			t.Fatalf("calendar interval/recipient: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("no calendar event")
	}
	b, err := booking.New(db).Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if b.SchedulingInvitationID != inv.ID || b.HostID != "selected" || b.EndAt.Sub(b.StartAt) != time.Hour || b.LocationValue == "Changed room" {
		t.Fatalf("bad snapshot booking %+v", b)
	}
	var recipient, answer string
	if err := db.QueryRow(`SELECT email FROM booking_attendees WHERE booking_id=? AND is_organizer=1`, id).Scan(&recipient); err != nil || recipient != "customer@example.com" {
		t.Fatalf("recipient %q %v", recipient, err)
	}
	if err := db.QueryRow(`SELECT value FROM booking_answers WHERE booking_id=?`, id).Scan(&answer); err != nil || answer != "Support" {
		t.Fatalf("intake %q %v", answer, err)
	}
	rec = invitationPublic(h, h.InvitationPage, "GET", inv.Token, "", "")
	mustStatus(t, rec, 410, "booked page")
	if !strings.Contains(rec.Body.String(), "already been booked") {
		t.Fatal("missing booked state")
	}
	// Expiration never prevents management of an already-created booking.
	calendarCheckExec(t, db, `UPDATE scheduling_invitations SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, inv.ID)
	manage, err := booking.New(db).IssueManageToken(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	rec = invitationPublic(h, h.ManagePage, "GET", manage, "", "")
	mustStatus(t, rec, 200, "manage page")
	if !strings.Contains(rec.Body.String(), "1 hour") || !strings.Contains(rec.Body.String(), "/manage/"+manage+"/slots") {
		t.Fatal("manage page did not retain effective duration/slots")
	}
	rec = invitationPublic(h, h.GetManageInvitationSlots, "GET", manage, "/slots?from="+day.Format("2006-01-02")+"&to="+day.Format("2006-01-02")+"&tz=UTC", "")
	mustStatus(t, rec, 200, "manage slots")
	var result invitationSlotsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Slots) != 3 {
		t.Fatalf("manage slots = %+v", result)
	}
	move := func(start time.Time) *httptest.ResponseRecorder {
		return invitationPublic(h, h.RescheduleByToken, "POST", manage, "/reschedule", fmt.Sprintf(`{"start_at":%q}`, start.Format(time.RFC3339)))
	}
	mustStatus(t, move(day.Add(12*time.Hour)), 409, "upper window enforced")
	mustStatus(t, move(day.Add(11*time.Hour)), 200, "end exactly at upper bound")
	waitInvitationQuery(t, db, `SELECT COUNT(*) FROM webhook_deliveries WHERE booking_id=? AND event='booking.rescheduled'`, id)
	select {
	case interval := <-p.moved:
		if interval.Start != day.Add(11*time.Hour) || interval.End != day.Add(12*time.Hour) {
			t.Fatal("wrong moved calendar interval", interval)
		}
	case <-time.After(time.Second):
		t.Fatal("calendar not moved")
	}
	manage, err = booking.New(db).IssueManageToken(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	rec = invitationPublic(h, h.CancelByToken, "POST", manage, "/cancel", `{"reason":"Resolved"}`)
	mustStatus(t, rec, 200, "cancel booking")
	waitInvitationQuery(t, db, `SELECT COUNT(*) FROM webhook_deliveries WHERE booking_id=? AND event='booking.cancelled'`, id)
	var status string
	if err := db.QueryRow(`SELECT status FROM scheduling_invitations WHERE id=?`, inv.ID).Scan(&status); err != nil || status != "booked" {
		t.Fatalf("cancellation reopened invitation: %s %v", status, err)
	}
	mustStatus(t, invitationSubmit(h, inv.Token, day.Add(10*time.Hour)), 410, "cancelled booking does not reopen invitation")
	if err := svc.ProcessSchedulingInvitations(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"booking.created", "booking.rescheduled", "booking.cancelled", "scheduling_invitation.booked"} {
		var raw string
		if err := db.QueryRow(`SELECT payload FROM webhook_deliveries WHERE event=? AND booking_id=?`, event, id).Scan(&raw); err != nil {
			t.Fatal(event, err)
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			t.Fatal(err)
		}
		data := envelope.Data
		if data["scheduling_invitation_id"] != inv.ID || data["booking_id"] != id || data["event_type_id"] != etID || data["duration_minutes"] != float64(60) || data["external"].(map[string]any)["reference"] != "ticket-123" {
			t.Fatalf("lost correlation %s: %s", event, raw)
		}
		if strings.Contains(raw, inv.Token) || strings.Contains(raw, "/s/") {
			t.Fatal("credential in webhook")
		}
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_invitation_events WHERE invitation_id=? AND event='scheduling_invitation.expired'`, inv.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("booked invitation expired", pending, err)
	}
}
