package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

// ArchiveUser handles POST /v1/users/{id}/archive — admin only.
//
// Archiving is the offboarding path (soft-delete): the user row and all its
// links are preserved, but the member can no longer log in, is hidden from the
// default member list, is skipped in routing, and their event types are
// deactivated. Reversible via RestoreUser.
//
// Guards mirror removal: cannot archive the owner (transfer first) or yourself;
// only the owner may archive an admin; archiving is blocked (409) while the
// member still has upcoming bookings as host — those must be reassigned or
// cancelled first (the admin UI resolves them per-meeting before archiving).
func (h *Handler) ArchiveUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		h.writeError(w, http.StatusBadRequest, "you cannot archive your own account")
		return
	}

	var targetIsAdmin, targetIsOwner int
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_admin, is_owner, archived_at FROM users WHERE id = ?`, targetID).
		Scan(&targetIsAdmin, &targetIsOwner, &archivedAt)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "archive user: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if archivedAt.Valid {
		h.writeError(w, http.StatusBadRequest, "member is already archived")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusBadRequest, "cannot archive the workspace owner; transfer ownership first")
		return
	}
	if targetIsAdmin != 0 && !actor.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can archive an admin")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var upcoming int
	if err := h.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM bookings
		WHERE host_id = ? AND status != 'cancelled' AND end_at > ?`,
		targetID, now).Scan(&upcoming); err != nil {
		h.logger.ErrorContext(r.Context(), "archive user: count bookings", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if upcoming > 0 {
		h.writeError(w, http.StatusConflict,
			fmt.Sprintf("this member has %d upcoming booking(s); reassign or cancel them before archiving", upcoming))
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "archive user: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(r.Context(),
		`UPDATE users SET archived_at = ?, archived_by = ? WHERE id = ?`, now, actor.ID, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "archive user: set archived_at", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Deactivate their event types so public booking pages stop taking bookings.
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE event_types SET is_active = 0 WHERE user_id = ?`, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "archive user: deactivate event types", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "archive user: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "archived_at": now})
}

// RestoreUser handles POST /v1/users/{id}/restore — admin only (owner required
// to restore an archived admin, mirroring archive). Clears archived_at so the
// member can log in again. Event types are NOT auto-reactivated — the admin
// re-enables any that should go live again.
func (h *Handler) RestoreUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	targetID := r.PathValue("id")

	var targetIsAdmin int
	var archivedAt, archivedBy sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_admin, archived_at, archived_by FROM users WHERE id = ?`, targetID).
		Scan(&targetIsAdmin, &archivedAt, &archivedBy)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "restore user: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !archivedAt.Valid {
		h.writeError(w, http.StatusBadRequest, "member is not archived")
		return
	}
	// Restore gating: the owner can restore anyone; an admin can restore only
	// members they archived themselves (and never an admin — that's owner-only).
	if !actor.IsOwner {
		if targetIsAdmin != 0 {
			h.writeError(w, http.StatusForbidden, "only the workspace owner can restore an admin")
			return
		}
		if !archivedBy.Valid || archivedBy.String != actor.ID {
			h.writeError(w, http.StatusForbidden, "you can only restore members you archived; ask the owner to restore this one")
			return
		}
	}

	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET archived_at = NULL, archived_by = NULL WHERE id = ?`, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "restore user: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
