package handler

import (
	"database/sql"
	"net/http"
	"time"
)

// ListUsers handles GET /v1/users — admin only. Returns all users.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	// Archived members are hidden unless explicitly requested (?include_archived=true).
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	where := "WHERE u.archived_at IS NULL"
	if includeArchived {
		where = ""
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT u.id, u.email, u.name, u.iana_timezone, u.is_admin, u.is_owner, u.email_login,
		       COALESCE(u.provider,''), COALESCE(u.avatar_url,''), u.created_at,
		       u.archived_at, COALESCE(u.archived_by,''), COALESCE(ab.name,'')
		FROM users u LEFT JOIN users ab ON ab.id = u.archived_by
		`+where+` ORDER BY u.created_at ASC`)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type teamRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type userRow struct {
		ID         string    `json:"id"`
		Email      string    `json:"email"`
		Name       string    `json:"name"`
		Timezone   string    `json:"timezone"`
		IsAdmin    bool      `json:"is_admin"`
		IsOwner    bool      `json:"is_owner"`
		Role       string    `json:"role"` // "owner" | "admin" | "member"
		EmailLogin bool      `json:"email_login"`
		Provider   string    `json:"provider,omitempty"`
		AvatarURL  string    `json:"avatar_url,omitempty"`
		CreatedAt      string    `json:"created_at"`
		Archived       bool      `json:"archived"`
		ArchivedAt     string    `json:"archived_at,omitempty"`
		ArchivedBy     string    `json:"archived_by,omitempty"`
		ArchivedByName string    `json:"archived_by_name,omitempty"`
		Teams          []teamRef `json:"teams"`
	}
	out := []userRow{}
	byID := map[string]*userRow{}
	for rows.Next() {
		var u userRow
		var isAdmin, isOwner, emailLogin int
		var archivedAt sql.NullString
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Timezone, &isAdmin, &isOwner, &emailLogin,
			&u.Provider, &u.AvatarURL, &u.CreatedAt, &archivedAt, &u.ArchivedBy, &u.ArchivedByName); err != nil {
			continue
		}
		u.IsAdmin = isAdmin != 0
		u.IsOwner = isOwner != 0
		u.EmailLogin = emailLogin != 0
		u.Archived = archivedAt.Valid
		u.ArchivedAt = archivedAt.String
		u.Teams = []teamRef{}
		switch {
		case u.IsOwner:
			u.Role = "owner"
		case u.IsAdmin:
			u.Role = "admin"
		default:
			u.Role = "member"
		}
		out = append(out, u)
	}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}

	// Attach each member's teams (the Members↔Teams cross-reference).
	tmRows, err := h.db.QueryContext(r.Context(), `
		SELECT tm.user_id, t.id, t.name
		FROM team_members tm JOIN teams t ON t.id = tm.team_id
		ORDER BY t.name ASC`)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users: team memberships", "error", err)
	} else {
		defer tmRows.Close()
		for tmRows.Next() {
			var userID string
			var tr teamRef
			if err := tmRows.Scan(&userID, &tr.ID, &tr.Name); err != nil {
				continue
			}
			if u := byID[userID]; u != nil {
				u.Teams = append(u.Teams, tr)
			}
		}
	}

	h.writeJSON(w, http.StatusOK, out)
}

// DeleteUser handles DELETE /v1/users/{id} — admin only.
//
// Guards (PRD §8.10):
//   - cannot delete yourself;
//   - the owner cannot be removed (transfer ownership first);
//   - only the owner may remove another admin;
//   - removal is blocked while the user still has upcoming bookings as host —
//     no silent orphaning; those must be reassigned or cancelled first.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	targetID := r.PathValue("id")
	if targetID == admin.ID {
		h.writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}

	var targetIsAdmin, targetIsOwner int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_admin, is_owner FROM users WHERE id = ?`, targetID).
		Scan(&targetIsAdmin, &targetIsOwner)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete user: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusBadRequest, "cannot remove the workspace owner; transfer ownership first")
		return
	}
	if targetIsAdmin != 0 && !admin.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can remove an admin")
		return
	}

	// Block removal while the user still hosts upcoming bookings, so attendees
	// are never silently orphaned. They must be reassigned or cancelled first.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var upcoming int
	if err := h.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM bookings
		WHERE host_id = ? AND status != 'cancelled' AND end_at > ?`,
		targetID, now).Scan(&upcoming); err != nil {
		h.logger.ErrorContext(r.Context(), "delete user: count bookings", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if upcoming > 0 {
		h.writeError(w, http.StatusConflict,
			"this member still has upcoming bookings; reassign or cancel them before removing the member")
		return
	}

	res, err := h.db.ExecContext(r.Context(), `DELETE FROM users WHERE id = ?`, targetID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete user: exec", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
