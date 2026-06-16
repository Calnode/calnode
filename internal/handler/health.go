package handler

import (
	"encoding/json"
	"net/http"

	"github.com/calnode/calnode/internal/buildinfo"
	"github.com/calnode/calnode/internal/db"
)

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Version reports the running binary's build identity (version, commit, build
// time). Public + unauthenticated so a control plane can verify which image a
// tenant is running during fleet rollouts.
func (h *Handler) Version(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, buildinfo.Get())
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

	// Gate readiness on migrations: report not-ready until the schema is at the
	// embedded target version, so a provisioner polling /readyz never routes
	// traffic to an instance still mid-migration (or one that failed to migrate).
	ready, err := db.SchemaReady(r.Context(), h.db)
	if err != nil || !ready {
		if err != nil {
			h.logger.ErrorContext(r.Context(), "readyz: migration check failed", "error", err)
		}
		h.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "error",
			"detail": "migrations pending",
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": buildinfo.Get(),
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Error("writeJSON: failed to encode response", "error", err)
	}
}
