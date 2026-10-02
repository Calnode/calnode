package worker_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
)

func TestInvitationSignedLifecycleAndBookingEvents(t *testing.T) {
	db, svc := setup(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	received := make(chan struct {
		header http.Header
		body   []byte
	}, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- struct {
			header http.Header
			body   []byte
		}{r.Header.Clone(), body}
		w.WriteHeader(200)
	}))
	defer server.Close()
	events := []string{"scheduling_invitation.created", "scheduling_invitation.booked", "scheduling_invitation.cancelled", "scheduling_invitation.expired", "booking.created", "booking.rescheduled", "booking.cancelled"}
	_, secret, err := svc.Create(ctx, "host-01", server.URL, events)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('event','host-01','support','Support',30)`)
	for _, id := range []string{"expired", "cancelled", "booked"} {
		exec(`INSERT INTO scheduling_invitations (id,event_type_id,created_by,recipient_email,duration_minutes,slot_interval_minutes,buffer_before_minutes,buffer_after_minutes,min_notice_minutes,max_future_days,routing_mode,rr_strategy,expires_at,external_system,external_reference,external_url,updated_at) VALUES (?,'event','host-01','recipient@example.com',60,15,0,0,0,30,'fixed','even','2099-01-01T00:00:00Z','tickets',?,'https://tickets.example/42','2026-01-01T00:00:00Z')`, id, "ticket-"+id)
		exec(`INSERT INTO scheduling_invitation_tokens (token_hash,invitation_id,expires_at) VALUES (?,?,'2099-01-01T00:00:00Z')`, fmt.Sprintf("%064s", id), id)
	}
	exec(`UPDATE scheduling_invitations SET expires_at='2000-01-01T00:00:00Z' WHERE id='expired'`)
	exec(`UPDATE scheduling_invitations SET status='cancelled',updated_at='2026-01-02T00:00:00Z' WHERE id='cancelled'`)
	exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status,location_type,location_value,scheduling_invitation_id) VALUES ('booking','event','host-02','2027-01-01T10:00:00Z','2027-01-01T11:00:00Z','confirmed','teams','https://meeting.example/room','booked')`)
	exec(`INSERT INTO booking_hosts (booking_id,user_id,is_primary) VALUES ('booking','host-02',1)`)
	exec(`UPDATE scheduling_invitations SET status='booked',booking_id='booking',updated_at='2026-01-02T00:00:00Z',expires_at='2000-01-01T00:00:00Z' WHERE id='booked'`)
	for _, event := range []string{"booking.created", "booking.rescheduled", "booking.cancelled"} {
		if err := svc.Enqueue(ctx, event, webhook.BookingPayload{ID: "booking", HostID: "host-02", StartAt: "2027-01-01T10:00:00Z", EndAt: "2027-01-01T11:00:00Z", Status: "confirmed"}); err != nil {
			t.Fatal(err)
		}
	}
	w := newWorker(t, db, svc)
	w.Poll(ctx)
	w.Poll(ctx)
	counts := map[string]int{}
	for len(received) > 0 {
		request := <-received
		if request.header.Get("X-Calnode-Signature") != webhook.Sign(key, request.body) || request.header.Get("X-Calnode-Delivery") == "" {
			t.Fatal("unsigned lifecycle event")
		}
		var envelope struct {
			ID    string         `json:"id"`
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := json.Unmarshal(request.body, &envelope); err != nil {
			t.Fatal(err)
		}
		counts[envelope.Event]++
		if envelope.ID == "" || envelope.Data["external"].(map[string]any)["system"] != "tickets" {
			t.Fatal("missing identity/correlation", string(request.body))
		}
		if strings.Contains(string(request.body), "token_hash") || strings.Contains(string(request.body), "scheduling_url") || strings.Contains(string(request.body), "/s/") {
			t.Fatal("credential in signed event")
		}
		if strings.HasPrefix(envelope.Event, "booking.") || envelope.Event == "scheduling_invitation.booked" {
			if envelope.Data["scheduling_invitation_id"] != "booked" || envelope.Data["booking_id"] != "booking" || envelope.Data["event_type_id"] != "event" || envelope.Data["duration_minutes"] != float64(60) || envelope.Data["start_at"] != "2027-01-01T10:00:00Z" || envelope.Data["end_at"] != "2027-01-01T11:00:00Z" || envelope.Data["location_value"] != "https://meeting.example/room" {
				t.Fatal("missing booking fields", string(request.body))
			}
			hosts := envelope.Data["hosts"].([]any)
			if len(hosts) != 1 || hosts[0].(map[string]any)["id"] != "host-02" {
				t.Fatal("missing assigned hosts", hosts)
			}
		}
		if envelope.Event == "scheduling_invitation.created" && envelope.Data["status"] != "active" {
			t.Fatal("creation reconstructed from terminal state")
		}
	}
	for _, event := range events {
		want := 1
		if event == "scheduling_invitation.created" {
			want = 3
		}
		if counts[event] != want {
			t.Fatalf("%s deliveries=%d want=%d", event, counts[event], want)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_invitation_events WHERE event='scheduling_invitation.expired'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate expiration: %d %v", count, err)
	}
}
