package handler

import (
	"net/http"
	"strings"
)

// stateSep separates the provider name from the userID inside the (encrypted)
// OAuth state, so the shared callback can route to the right provider.
const stateSep = "\x1f"

// ConnectCalendar handles GET /v1/calendar/connect (auth required).
// Redirects the browser to the chosen provider's OAuth consent page.
// Optional ?provider=<name> selects a provider; defaults to the primary.
func (h *Handler) ConnectCalendar(w http.ResponseWriter, r *http.Request) {
	svc := h.getCal()
	if svc == nil || !svc.Any() {
		h.writeError(w, http.StatusNotImplemented, "Calendar integration not configured")
		return
	}
	p := svc.Primary()
	if name := r.URL.Query().Get("provider"); name != "" {
		if pr := svc.Provider(name); pr != nil {
			p = pr
		} else {
			h.writeError(w, http.StatusBadRequest, "unknown calendar provider")
			return
		}
	}
	user, _ := userFromContext(r.Context())
	// Encode the provider in the state so the shared callback routes correctly.
	state, err := p.EncryptState(p.Name() + stateSep + user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "calendar connect: encrypt state", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.Redirect(w, r, p.AuthURL(state), http.StatusFound)
}

// CalendarCallback handles GET /v1/calendar/callback (public — browser redirect from the provider).
// Validates the encrypted state, exchanges the auth code, and persists tokens.
//
// Phase 1 resolves to the primary provider (Google). When a second provider is added,
// the provider will be encoded in the state and resolved here.
func (h *Handler) CalendarCallback(w http.ResponseWriter, r *http.Request) {
	svc := h.getCal()
	if svc == nil || !svc.Any() {
		h.writeError(w, http.StatusNotImplemented, "Calendar integration not configured")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.writeError(w, http.StatusBadRequest, "OAuth error: "+errParam)
		return
	}

	// All providers share the encryption key, so any provider's DecryptState
	// recovers the state; we then resolve the provider it encodes.
	raw, err := svc.Primary().DecryptState(r.URL.Query().Get("state"))
	if err != nil || raw == "" {
		h.writeError(w, http.StatusBadRequest, "invalid or missing state")
		return
	}
	p := svc.Primary()
	userID := raw
	if i := strings.Index(raw, stateSep); i >= 0 {
		if pr := svc.Provider(raw[:i]); pr != nil {
			p = pr
		}
		userID = raw[i+1:]
	}
	if userID == "" {
		h.writeError(w, http.StatusBadRequest, "invalid or missing state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	if err := p.Exchange(r.Context(), userID, code, "primary"); err != nil {
		h.logger.ErrorContext(r.Context(), "calendar callback: exchange", "error", err, "user_id", userID)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.Redirect(w, r, h.baseURL+"/admin/calendar?connected=true", http.StatusFound)
}

// CalendarStatus handles GET /v1/calendar/status (auth required).
func (h *Handler) CalendarStatus(w http.ResponseWriter, r *http.Request) {
	svc := h.getCal()
	if svc == nil || !svc.Any() {
		h.writeJSON(w, http.StatusOK, map[string]any{"connected": false, "configured": false})
		return
	}
	user, _ := userFromContext(r.Context())
	connected, provider, err := svc.Connected(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "calendar status", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{"connected": connected, "configured": true}
	if connected {
		resp["provider"] = provider
	}
	h.writeJSON(w, http.StatusOK, resp)
}

// DisconnectCalendar handles DELETE /v1/calendar (auth required).
func (h *Handler) DisconnectCalendar(w http.ResponseWriter, r *http.Request) {
	svc := h.getCal()
	if svc == nil || !svc.Any() {
		h.writeError(w, http.StatusNotImplemented, "Calendar integration not configured")
		return
	}
	user, _ := userFromContext(r.Context())
	if err := svc.Disconnect(r.Context(), user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "calendar disconnect", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
