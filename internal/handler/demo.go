package handler

import (
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/demo"
)

// DemoEnter handles GET /v1/demo/enter — signs the visitor straight into the
// seeded demo owner account (no password to type or display) and redirects
// into the admin UI. Only registered when the instance is running in demo
// mode (see internal/server/server.go).
func (h *Handler) DemoEnter(w http.ResponseWriter, r *http.Request) {
	if err := h.createSession(r.Context(), w, demo.OwnerUserID); err != nil {
		h.logger.ErrorContext(r.Context(), "demo: create session", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

// DemoReset handles POST /v1/demo/reset — wipes and re-seeds the database on
// demand, in addition to the periodic reset ticker started in BuildHandler.
// The caller rate-limits this route.
func (h *Handler) DemoReset(w http.ResponseWriter, r *http.Request) {
	if err := demo.Reset(r.Context(), h.db); err != nil {
		h.logger.ErrorContext(r.Context(), "demo: manual reset", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.SetDemoNextResetAt(time.Now().Add(h.demoResetInterval))
	h.writeJSON(w, http.StatusOK, map[string]any{"status": "reset"})
}

// Robots handles GET /robots.txt — disallow-all. Only registered in demo
// mode: the demo's data is public and ephemeral and shouldn't be indexed.
func (h *Handler) Robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
}
