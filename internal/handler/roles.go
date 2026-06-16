package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// SetUserRole handles PATCH /v1/users/{id}/role — owner only.
//
// Body: {"role": "admin" | "member"}. Promotes or demotes a user between the
// member and admin tiers. Per PRD §8.10 only the owner may grant or revoke
// admin. The owner's own role cannot be changed here (use transfer-ownership),
// and you cannot change your own role.
func (h *Handler) SetUserRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can change roles")
		return
	}
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		h.writeError(w, http.StatusBadRequest, "you cannot change your own role")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Role != "admin" && req.Role != "member" {
		h.writeError(w, http.StatusBadRequest, "role must be 'admin' or 'member'")
		return
	}

	var targetIsOwner int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_owner FROM users WHERE id = ?`, targetID).Scan(&targetIsOwner)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "set role: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusBadRequest, "cannot change the owner's role; transfer ownership first")
		return
	}

	isAdmin := 0
	if req.Role == "admin" {
		isAdmin = 1
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET is_admin = ? WHERE id = ?`, isAdmin, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "set role: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"id": targetID, "role": req.Role})
}

// TransferOwnership handles POST /v1/users/{id}/transfer-ownership — owner only.
//
// Moves the single owner tier to the target user (who also becomes admin), and
// demotes the current owner to admin. Exactly one owner exists at all times;
// the swap runs in one transaction so that invariant never breaks.
func (h *Handler) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can transfer ownership")
		return
	}
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		h.writeError(w, http.StatusBadRequest, "you are already the owner")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var exists int
	if err := tx.QueryRowContext(r.Context(),
		`SELECT 1 FROM users WHERE id = ?`, targetID).Scan(&exists); err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Demote the current owner to admin, promote the target to owner+admin.
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE users SET is_owner = 0 WHERE id = ?`, actor.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: demote", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE users SET is_owner = 1, is_admin = 1 WHERE id = ?`, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: promote", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "new_owner_id": targetID})
}
