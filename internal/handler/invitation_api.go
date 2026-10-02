package handler

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// ListSchedulingInvitations uses the existing items/limit/offset convention,
// scoped to the creator, including terminal records for integration auditing.
func (h *Handler) ListSchedulingInvitations(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	limit, offset := 25, 0
	for _, f := range []struct {
		key  string
		dest *int
	}{{"limit", &limit}, {"offset", &offset}} {
		if value := r.URL.Query().Get(f.key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || (f.key == "limit" && (n == 0 || n > 100)) {
				h.writeError(w, 400, "limit must be 1..100 and offset must be non-negative")
				return
			}
			*f.dest = n
		}
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id FROM scheduling_invitations WHERE created_by = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, user.ID, limit, offset)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			h.invitationError(w, r, err)
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	items := []*schedulingInvitation{}
	for _, id := range ids {
		inv, err := h.loadSchedulingInvitation(r.Context(), id, user.ID)
		if err != nil {
			h.invitationError(w, r, err)
			return
		}
		items = append(items, inv)
	}
	var total int
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM scheduling_invitations WHERE created_by = ?`, user.ID).Scan(&total); err != nil {
		h.invitationError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) CancelSchedulingInvitation(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	defer tx.Rollback()
	var status string
	id := r.PathValue("id")
	if err := tx.QueryRowContext(r.Context(), `SELECT status FROM scheduling_invitations WHERE id = ? AND created_by = ?`, id, user.ID).Scan(&status); err != nil {
		h.invitationError(w, r, err)
		return
	}
	if status == "cancelled" {
		h.writeJSON(w, 200, map[string]string{"id": id, "status": status})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(r.Context(), `UPDATE scheduling_invitations SET status = 'cancelled', updated_at = ? WHERE id = ? AND status = 'active' AND booking_id IS NULL AND julianday(expires_at)>julianday(?)`, now, id, now)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		h.invitationError(w, r, errInvitationUnavailable)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE scheduling_invitation_tokens SET consumed_at = ? WHERE invitation_id = ? AND consumed_at IS NULL`, now, id); err != nil {
		h.invitationError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.invitationError(w, r, err)
		return
	}
	h.GetSchedulingInvitation(w, r)
}

// dbQuerier permits local rechecks on either the pool or its serialized transaction.
type dbQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
