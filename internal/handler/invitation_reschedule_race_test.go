package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Change local availability during the external-calendar check, after the
// preliminary local validation but before the transaction's authoritative one.
type rescheduleRaceCalendar struct {
	calendar.Provider
	db    *sql.DB
	owner string
	calls int
}

func (p *rescheduleRaceCalendar) Name() string { return "google" }
func (p *rescheduleRaceCalendar) FreeBusy(ctx context.Context, _ string, _, _ time.Time) ([]slots.Interval, error) {
	p.calls++
	_, err := p.db.ExecContext(ctx, `DELETE FROM availability_rules WHERE user_id=?`, p.owner)
	return nil, err
}

func TestInvitationRescheduleRaceReturnsConflict(t *testing.T) {
	for _, surface := range []string{"manage", "authenticated", "mcp"} {
		t.Run(surface, func(t *testing.T) {
			h, db, key, owner, slug, _, day := invitationFixture(t)
			rec, inv := createInvitation(t, h, key, invitationBody(slug))
			mustStatus(t, rec, 201, "create invitation")
			rec = invitationSubmit(h, inv.Token, day.Add(9*time.Hour))
			id := mustString(t, mustJSON(t, rec, 201, "book"), "id", "book")
			waitInvitationConfirmation(t, db, id)
			provider := &rescheduleRaceCalendar{db: db, owner: owner}
			cal := calendar.NewService(db)
			cal.Register(provider)
			h.SetCalendar(cal)
			start := day.Add(10 * time.Hour)
			body := fmt.Sprintf(`{"start_at":%q}`, start.Format(time.RFC3339))
			switch surface {
			case "manage":
				token, err := booking.New(db).IssueManageToken(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				rec = invitationPublic(h, h.RescheduleByToken, "POST", token, "/reschedule", body)
				mustStatus(t, rec, 409, "transaction rejected lost slot")
			case "authenticated":
				req := authReq("PATCH", "/v1/bookings/"+id+"/reschedule", body, key)
				req.SetPathValue("id", id)
				rec = httptest.NewRecorder()
				h.RequireAuth(h.RescheduleBooking)(rec, req)
				mustStatus(t, rec, 409, "transaction rejected lost slot")
			case "mcp":
				result, err := connectMCP(t, h).CallTool(t.Context(), &mcp.CallToolParams{Name: "reschedule_booking", Arguments: map[string]any{"booking_id": id, "new_slot_start": start.Format(time.RFC3339)}})
				if err != nil {
					t.Fatal(err)
				}
				content, err := json.Marshal(result.Content)
				if err != nil || !result.IsError || !strings.Contains(string(content), "that time slot is no longer available") {
					t.Fatalf("unexpected MCP race result: %s %v", content, err)
				}
			}
			if provider.calls != 1 {
				t.Fatalf("race not exercised: calendar calls=%d", provider.calls)
			}
			b, err := booking.New(db).Get(t.Context(), id)
			if err != nil || !b.StartAt.Equal(day.Add(9*time.Hour)) || b.SchedulingInvitationID != inv.ID {
				t.Fatalf("rejected move changed booking: %+v %v", b, err)
			}
		})
	}
}
