package handler

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	if err := h.db.PingContext(r.Context()); err != nil {
		h.logger.ErrorContext(r.Context(), "readyz: database ping failed", "error", err)
		h.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "error",
			"detail": "database unavailable",
		})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Error("writeJSON: failed to encode response", "error", err)
	}
}
