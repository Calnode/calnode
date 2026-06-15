package handler

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/secret"
)

// GoogleOAuthConfig holds decrypted Google OAuth settings loaded from the DB.
// Used by server.go to build the initial gcal and auth clients on startup.
type GoogleOAuthConfig struct {
	ClientID     string
	ClientSecret string
}

// LoadGoogleSettingsFromDB reads Google OAuth credentials from server_settings
// and decrypts the client secret. Returns nil (not an error) when client_id is empty.
func LoadGoogleSettingsFromDB(db *sql.DB, encKey [32]byte) (*GoogleOAuthConfig, error) {
	var clientID, secretEnc string
	err := db.QueryRow(`
		SELECT google_client_id, google_client_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc)
	if err == sql.ErrNoRows || clientID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var clientSecret string
	if secretEnc != "" {
		clientSecret, err = secret.Decrypt(encKey, secretEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt google client secret: %w", err)
		}
	}
	return &GoogleOAuthConfig{ClientID: clientID, ClientSecret: clientSecret}, nil
}

// GetGoogleSettings handles GET /v1/settings/google (admin only).
func (h *Handler) GetGoogleSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	var clientID, secretEnc string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT google_client_id, google_client_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "google settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"client_id":         clientID,
		"client_secret_set": secretEnc != "",
		"configured":        clientID != "",
	})
}

// PatchGoogleSettings handles PATCH /v1/settings/google (admin only).
// Saves credentials to the DB and hot-reloads the gcal and Google auth clients
// so changes take effect immediately without a server restart.
// If client_secret is omitted or empty the existing stored secret is kept.
func (h *Handler) PatchGoogleSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.ClientSecret != "" {
		enc, err := secret.Encrypt(h.encKey, req.ClientSecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "google settings: encrypt secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err = h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  google_client_id = ?, google_client_secret_enc = ?,
			  updated_at = datetime('now')
			WHERE id = 1`, req.ClientID, enc); err != nil {
			h.logger.ErrorContext(r.Context(), "google settings: update", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  google_client_id = ?,
			  updated_at = datetime('now')
			WHERE id = 1`, req.ClientID); err != nil {
			h.logger.ErrorContext(r.Context(), "google settings: update (keep secret)", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Hot-reload: resolve the active secret and reinitialise gcal + Google auth
	// so changes take effect without a server restart.
	resolvedSecret := req.ClientSecret
	if resolvedSecret == "" && req.ClientID != "" {
		var secretEnc string
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT google_client_secret_enc FROM server_settings WHERE id = 1`).
			Scan(&secretEnc); err == nil && secretEnc != "" {
			resolvedSecret, _ = secret.Decrypt(h.encKey, secretEnc)
		}
	}

	if req.ClientID != "" && resolvedSecret != "" {
		encKeyHex := hex.EncodeToString(h.encKey[:])
		if gc, err := gcal.New(h.db, req.ClientID, resolvedSecret, h.baseURL+"/v1/calendar/callback", encKeyHex); err != nil {
			h.logger.ErrorContext(r.Context(), "google settings: hot-reload gcal failed", "error", err)
		} else {
			h.SetCalendar(gc)
		}
		h.SetGoogleAuth(req.ClientID, resolvedSecret, h.baseURL+"/v1/auth/callback", h.secureCookie)
		h.logger.Info("google settings: credentials updated and gcal hot-reloaded")
	} else if req.ClientID == "" {
		h.SetCalendar(nil)
		h.googleAuth = nil
	}

	h.GetGoogleSettings(w, r)
}
