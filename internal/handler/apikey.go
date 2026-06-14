package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// ListAPIKeys handles GET /v1/api-keys.
func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, name, created_at, last_used_at
		FROM api_keys WHERE user_id = ?
		ORDER BY created_at DESC`, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list api keys", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type keyItem struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		CreatedAt  string  `json:"created_at"`
		LastUsedAt *string `json:"last_used_at"`
	}
	items := []keyItem{}
	for rows.Next() {
		var item keyItem
		if err := rows.Scan(&item.ID, &item.Name, &item.CreatedAt, &item.LastUsedAt); err != nil {
			h.logger.ErrorContext(r.Context(), "scan api key row", "error", err)
			continue
		}
		items = append(items, item)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateAPIKey handles POST /v1/api-keys.
// The plaintext key is returned once and must be saved by the caller.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.logger.ErrorContext(r.Context(), "create api key: rand", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	plainKey := "cno_" + hex.EncodeToString(raw)
	keyHash := hashAPIKey(plainKey)

	keyID := uid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO api_keys (id, user_id, name, key_hash, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		keyID, user.ID, req.Name, keyHash, now); err != nil {
		h.logger.ErrorContext(r.Context(), "create api key: insert", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":         keyID,
		"name":       req.Name,
		"key":        plainKey,
		"created_at": now,
		"note":       "save this key — it will not be shown again",
	})
}

// DeleteAPIKey handles DELETE /v1/api-keys/{id}.
func (h *Handler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")

	res, err := h.db.ExecContext(r.Context(), `
		DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete api key", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
