package handler

import (
	"net/http"
)

// ListUsers handles GET /v1/users — admin only. Returns all users.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, email, name, iana_timezone, is_admin, is_owner, email_login,
		       COALESCE(provider,''), COALESCE(avatar_url,''), created_at
		FROM users ORDER BY created_at ASC`)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type userRow struct {
		ID         string `json:"id"`
		Email      string `json:"email"`
		Name       string `json:"name"`
		Timezone   string `json:"timezone"`
		IsAdmin    bool   `json:"is_admin"`
		IsOwner    bool   `json:"is_owner"`
		Role       string `json:"role"` // "owner" | "admin" | "member"
		EmailLogin bool   `json:"email_login"`
		Provider   string `json:"provider,omitempty"`
		AvatarURL  string `json:"avatar_url,omitempty"`
		CreatedAt  string `json:"created_at"`
	}
	var out []userRow
	for rows.Next() {
		var u userRow
		var isAdmin, isOwner, emailLogin int
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Timezone, &isAdmin, &isOwner, &emailLogin,
			&u.Provider, &u.AvatarURL, &u.CreatedAt); err != nil {
			continue
		}
		u.IsAdmin = isAdmin != 0
		u.IsOwner = isOwner != 0
		u.EmailLogin = emailLogin != 0
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
	if out == nil {
		out = []userRow{}
	}
	h.writeJSON(w, http.StatusOK, out)
}

// DeleteUser handles DELETE /v1/users/{id} — admin only.
// Cannot delete yourself.
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
