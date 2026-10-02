package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

type invitationQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Correlation is mandatory for invitation booking events, independently of the
// optional field selection used for ordinary booking/attendee data.
func (s *Service) bookingInvitationCorrelation(ctx context.Context, source invitationQuerier, id string) (map[string]any, error) {
	var invID, eventID, system, reference, url, locationType, locationValue, start, end string
	var duration int
	err := source.QueryRowContext(ctx, `SELECT i.id,i.event_type_id,i.duration_minutes,i.external_system,i.external_reference,i.external_url,b.location_type,COALESCE(b.location_value,''),b.start_at,b.end_at FROM bookings b JOIN scheduling_invitations i ON i.id=b.scheduling_invitation_id WHERE b.id=?`, id).Scan(&invID, &eventID, &duration, &system, &reference, &url, &locationType, &locationValue, &start, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hosts := []map[string]any{}
	rows, err := source.QueryContext(ctx, `SELECT bh.user_id,u.name,bh.is_primary FROM booking_hosts bh JOIN users u ON u.id=bh.user_id WHERE bh.booking_id=? ORDER BY bh.is_primary DESC,bh.user_id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var hostID, name string
		var primary bool
		if err := rows.Scan(&hostID, &name, &primary); err != nil {
			rows.Close()
			return nil, err
		}
		hosts = append(hosts, map[string]any{"id": hostID, "name": name, "is_primary": primary})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return map[string]any{"scheduling_invitation_id": invID, "booking_id": id, "event_type_id": eventID, "duration_minutes": duration, "start_at": start, "end_at": end, "hosts": hosts, "location_type": locationType, "location_value": locationValue, "external": map[string]string{"system": system, "reference": reference, "url": url}}, nil
}

// ProcessSchedulingInvitations atomically expires unconsumed invitations, then
// drains their transactional outbox into the existing delivery/job mechanism.
// Network delivery happens later, outside this transaction. A replayed poll
// cannot produce a second expiration transition or duplicate delivery rows.
func (s *Service) ProcessSchedulingInvitations(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE scheduling_invitations SET status='expired',updated_at=? WHERE status='active' AND booking_id IS NULL AND julianday(expires_at)<=julianday(?)`, now, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.invitation_id,e.event,e.created_at,e.payload,i.created_by FROM scheduling_invitation_events e JOIN scheduling_invitations i ON i.id=e.invitation_id WHERE e.dispatched_at IS NULL ORDER BY e.created_at,e.id LIMIT 100`)
	if err != nil {
		return err
	}
	type pending struct{ id, invID, event, createdAt, payload, owner string }
	batch := []pending{}
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.invID, &p.event, &p.createdAt, &p.payload, &p.owner); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range batch {
		var data map[string]any
		if err := json.Unmarshal([]byte(p.payload), &data); err != nil {
			return err
		}
		var bookingID any
		if id, ok := data["booking_id"].(string); ok && id != "" {
			bookingID = id
			// Meeting creation is best effort after booking commit. Include the value
			// currently available at dispatch while retaining transition-time intervals.
			var location string
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(location_value,'') FROM bookings WHERE id=?`, id).Scan(&location); err != nil {
				return err
			}
			data["location_value"] = location
		}
		payload, err := json.Marshal(map[string]any{"event": p.event, "created_at": p.createdAt, "data": data, "id": p.id})
		if err != nil {
			return err
		}
		whRows, err := tx.QueryContext(ctx, `SELECT id,events FROM webhooks WHERE user_id=? AND is_active=1`, p.owner)
		if err != nil {
			return err
		}
		targets := []string{}
		for whRows.Next() {
			var id, raw string
			if err := whRows.Scan(&id, &raw); err != nil {
				whRows.Close()
				return err
			}
			var events []string
			if err := json.Unmarshal([]byte(raw), &events); err != nil {
				whRows.Close()
				return err
			}
			for _, event := range events {
				if event == p.event {
					targets = append(targets, id)
					break
				}
			}
		}
		err = whRows.Err()
		whRows.Close()
		if err != nil {
			return err
		}
		for _, whID := range targets {
			if err := insertDelivery(ctx, tx, whID, bookingID, p.event, payload, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduling_invitation_events SET dispatched_at=? WHERE id=? AND dispatched_at IS NULL`, now, p.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertDelivery(ctx context.Context, tx *sql.Tx, webhookID string, bookingID any, event string, payload []byte, now string) error {
	id := uid.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries (id,webhook_id,booking_id,event,payload,status) VALUES (?,?,?,?,?,'pending')`, id, webhookID, bookingID, event, string(payload)); err != nil {
		return err
	}
	jobPayload, _ := json.Marshal(map[string]string{"webhook_delivery_id": id})
	_, err := tx.ExecContext(ctx, `INSERT INTO jobs (id,type,payload,run_at) VALUES (?,'webhook.deliver',?,?)`, uid.New(), string(jobPayload), now)
	return err
}
