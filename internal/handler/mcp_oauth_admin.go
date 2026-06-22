package handler

import "net/http"

// Admin management of MCP OAuth connections — the apps a user has connected via the
// "Connect" flow. Scoped per-user (like API keys): you see and revoke the apps you
// authorized.

type oauthConnectionJSON struct {
	ID         string  `json:"id"`
	ClientName string  `json:"client_name"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	ExpiresAt  string  `json:"expires_at"`
}

// ListOAuthConnections handles GET /v1/oauth/connections.
func (h *Handler) ListOAuthConnections(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT t.id, COALESCE(c.client_name, ''), t.created_at, t.last_used_at, t.expires_at
		FROM oauth_access_tokens t
		LEFT JOIN oauth_clients c ON c.client_id = t.client_id
		WHERE t.user_id = ?
		ORDER BY t.created_at DESC`, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list oauth connections", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []oauthConnectionJSON{}
	for rows.Next() {
		var it oauthConnectionJSON
		if err := rows.Scan(&it.ID, &it.ClientName, &it.CreatedAt, &it.LastUsedAt, &it.ExpiresAt); err != nil {
			h.logger.ErrorContext(r.Context(), "scan oauth connection", "error", err)
			continue
		}
		if it.ClientName == "" {
			it.ClientName = "Unknown app"
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list oauth connections: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// RevokeOAuthConnection handles DELETE /v1/oauth/connections/{id}: it deletes the
// access/refresh token, immediately cutting off that app's access to /mcp.
func (h *Handler) RevokeOAuthConnection(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM oauth_access_tokens WHERE id = ? AND user_id = ?`, id, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke oauth connection", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "connection not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
