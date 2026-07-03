package handler

import "net/http"

// ConnectZoom handles GET /v1/zoom/connect (auth required). Redirects the host to Zoom's
// OAuth consent page so they can connect their own Zoom account.
func (h *Handler) ConnectZoom(w http.ResponseWriter, r *http.Request) {
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
		return
	}
	zc := h.getZoom()
	if zc == nil {
		h.writeError(w, http.StatusNotImplemented, "Zoom integration not configured")
		return
	}
	user, _ := userFromContext(r.Context())
	state, err := zc.EncryptState(user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "zoom connect: encrypt state", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.Redirect(w, r, zc.AuthURL(state), http.StatusFound)
}

// ZoomCallback handles GET /v1/zoom/callback (public — browser redirect from Zoom).
// Validates the encrypted state, exchanges the code, and persists the host's tokens.
func (h *Handler) ZoomCallback(w http.ResponseWriter, r *http.Request) {
	zc := h.getZoom()
	if zc == nil {
		h.writeError(w, http.StatusNotImplemented, "Zoom integration not configured")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.writeError(w, http.StatusBadRequest, "OAuth error: "+errParam)
		return
	}
	userID, err := zc.DecryptState(r.URL.Query().Get("state"))
	if err != nil || userID == "" {
		h.writeError(w, http.StatusBadRequest, "invalid or missing state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		h.writeError(w, http.StatusBadRequest, "missing code")
		return
	}
	if err := zc.Exchange(r.Context(), userID, code); err != nil {
		h.logger.ErrorContext(r.Context(), "zoom callback: exchange", "error", err, "user_id", userID)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.Redirect(w, r, h.baseURL+"/admin/calendar?zoom=connected", http.StatusFound)
}

// ZoomStatus handles GET /v1/zoom/status (auth required) — whether Zoom is configured for
// the instance and whether the current host has connected their account.
func (h *Handler) ZoomStatus(w http.ResponseWriter, r *http.Request) {
	zc := h.getZoom()
	if zc == nil {
		h.writeJSON(w, http.StatusOK, map[string]any{"configured": false, "connected": false})
		return
	}
	user, _ := userFromContext(r.Context())
	connected, err := zc.Connected(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "zoom status", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"configured": true, "connected": connected})
}

// DisconnectZoom handles DELETE /v1/zoom (auth required).
func (h *Handler) DisconnectZoom(w http.ResponseWriter, r *http.Request) {
	zc := h.getZoom()
	if zc == nil {
		h.writeError(w, http.StatusNotImplemented, "Zoom integration not configured")
		return
	}
	user, _ := userFromContext(r.Context())
	if err := zc.Disconnect(r.Context(), user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "zoom disconnect", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
