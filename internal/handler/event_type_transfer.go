package handler

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) TransferEventType(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		ExpectedOwnerID string `json:"expected_owner_id"`
		NewOwnerID      string `json:"new_owner_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedOwnerID != user.ID || body.NewOwnerID == "" || body.NewOwnerID == user.ID {
		h.writeError(w, http.StatusBadRequest, "expected current owner and a different new owner are required")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(r.Context(), `SELECT id FROM event_types WHERE slug = ? AND user_id = ? AND archived_at IS NULL`, r.PathValue("slug"), body.ExpectedOwnerID).Scan(&id)
	if err != nil {
		h.writeError(w, http.StatusConflict, "event ownership changed or event not found")
		return
	}
	var eligible int
	err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM event_type_hosts h JOIN users u ON u.id=h.user_id WHERE h.event_type_id=? AND h.user_id=? AND h.role='required' AND u.archived_at IS NULL`, id, body.NewOwnerID).Scan(&eligible)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if eligible != 1 {
		h.writeError(w, http.StatusConflict, "new owner must be an active required host")
		return
	}
	var active int
	err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM bookings WHERE event_type_id=? AND status!='cancelled' AND end_at > strftime('%Y-%m-%dT%H:%M:%SZ','now')`, id).Scan(&active)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if active > 0 {
		h.writeError(w, http.StatusConflict, "transfer requires no upcoming bookings")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE availability_rules SET user_id=? WHERE event_type_id=? AND user_id=?`, body.NewOwnerID, id, body.ExpectedOwnerID)
	if err != nil {
		h.writeError(w, http.StatusConflict, "event availability conflicts with new owner rules")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE event_types SET user_id=? WHERE id=? AND user_id=?`, body.NewOwnerID, id, body.ExpectedOwnerID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.logger.InfoContext(r.Context(), "event ownership transferred", "event_type_id", id, "previous_owner", user.ID, "new_owner", body.NewOwnerID)
	h.writeJSON(w, http.StatusOK, map[string]any{"id": id, "slug": r.PathValue("slug"), "owner_id": body.NewOwnerID})
}
