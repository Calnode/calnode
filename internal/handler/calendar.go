package handler

import (
	"net/http"
)

// ConnectCalendar handles GET /v1/calendar/connect (auth required).
// Redirects the browser to the Google OAuth consent page.
func (h *Handler) ConnectCalendar(w http.ResponseWriter, r *http.Request) {
	if h.gcal == nil {
		h.writeError(w, http.StatusNotImplemented, "Google Calendar not configured (set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET)")
		return
	}
	user, _ := userFromContext(r.Context())
	state, err := h.gcal.EncryptState(user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "calendar connect: encrypt state", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.Redirect(w, r, h.gcal.AuthURL(state), http.StatusFound)
}

// CalendarCallback handles GET /v1/calendar/callback (public — browser redirect from Google).
// Validates the encrypted state, exchanges the auth code, and persists tokens.
func (h *Handler) CalendarCallback(w http.ResponseWriter, r *http.Request) {
	if h.gcal == nil {
		h.writeError(w, http.StatusNotImplemented, "Google Calendar not configured")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.writeError(w, http.StatusBadRequest, "OAuth error: "+errParam)
		return
	}

	state := r.URL.Query().Get("state")
	userID, err := h.gcal.DecryptState(state)
	if err != nil || userID == "" {
		h.writeError(w, http.StatusBadRequest, "invalid or missing state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	if err := h.gcal.Exchange(r.Context(), userID, code, "primary"); err != nil {
		h.logger.ErrorContext(r.Context(), "calendar callback: exchange", "error", err, "user_id", userID)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Redirect to the app root; the UI can detect ?calendar=connected.
	http.Redirect(w, r, h.baseURL+"?calendar=connected", http.StatusFound)
}

// CalendarStatus handles GET /v1/calendar/status (auth required).
func (h *Handler) CalendarStatus(w http.ResponseWriter, r *http.Request) {
	if h.gcal == nil {
		h.writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	user, _ := userFromContext(r.Context())
	connected, err := h.gcal.Connected(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "calendar status", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{"connected": connected}
	if connected {
		resp["provider"] = "google"
	}
	h.writeJSON(w, http.StatusOK, resp)
}

// DisconnectCalendar handles DELETE /v1/calendar (auth required).
func (h *Handler) DisconnectCalendar(w http.ResponseWriter, r *http.Request) {
	if h.gcal == nil {
		h.writeError(w, http.StatusNotImplemented, "Google Calendar not configured")
		return
	}
	user, _ := userFromContext(r.Context())
	if err := h.gcal.Disconnect(r.Context(), user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "calendar disconnect", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
