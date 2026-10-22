package handler_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteUserRetainsInvitationReferences(t *testing.T) {
	// Isolate each reference from booking-host deletion guards. Even a booked
	// invitation can retain a creator or snapshot host who is not its primary host.
	for _, reference := range []string{"creator", "event owner", "required", "rotation", "optional"} {
		for _, status := range []string{"draft", "active", "booked", "cancelled", "expired"} {
			t.Run(reference+"/"+status, func(t *testing.T) {
				h, db, key, owner, _, eventID, _ := invitationFixture(t)
				calendarCheckExec(t, db, `INSERT INTO users (id,email,name) VALUES ('target','target@example.com','Target')`)
				creator := owner
				if reference == "creator" {
					creator = "target"
				}
				if reference == "event owner" {
					calendarCheckExec(t, db, `UPDATE event_types SET user_id='target' WHERE id=?`, eventID)
				}
				var bookingID any
				if status == "booked" {
					bookingID = "historical-booking"
					calendarCheckExec(t, db, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES (?,? ,?,'2020-01-01T09:00:00Z','2020-01-01T09:30:00Z','cancelled')`, bookingID, eventID, owner)
				}
				calendarCheckExec(t, db, `INSERT INTO scheduling_invitations (id,event_type_id,created_by,recipient_email,duration_minutes,slot_interval_minutes,buffer_before_minutes,buffer_after_minutes,min_notice_minutes,max_future_days,routing_mode,rr_strategy,expires_at,status,booking_id) VALUES ('retained',?,?,'customer@example.com',30,30,0,0,0,30,'fixed','even','2099-01-01T00:00:00Z',?,?)`, eventID, creator, status, bookingID)
				if reference == "required" || reference == "rotation" || reference == "optional" {
					calendarCheckExec(t, db, `INSERT INTO scheduling_invitation_hosts (invitation_id,user_id,role) VALUES ('retained','target',?)`, reference)
				}
				req := authReq("DELETE", "/v1/users/target", "", key)
				req.SetPathValue("id", "target")
				rec := httptest.NewRecorder()
				h.RequireAuth(h.DeleteUser)(rec, req)
				mustStatus(t, rec, 409, "retain invitation references")
				if !strings.Contains(rec.Body.String(), "scheduling invitations; archive them instead") {
					t.Fatal("missing deletion guidance")
				}
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id='target'`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("target deleted: %d %v", count, err)
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM scheduling_invitations WHERE id='retained' AND status=?`, status).Scan(&count); err != nil || count != 1 {
					t.Fatalf("invitation changed: %d %v", count, err)
				}
				req = authReq("POST", "/v1/users/target/archive", "", key)
				req.SetPathValue("id", "target")
				rec = httptest.NewRecorder()
				h.RequireAuth(h.ArchiveUser)(rec, req)
				mustStatus(t, rec, 200, "archive referenced user")
				if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id='target' AND archived_at IS NOT NULL`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("archive failed: %d %v", count, err)
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("references invalid: %d %v", count, err)
				}
			})
		}
	}
}
