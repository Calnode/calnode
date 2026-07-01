package handler

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/zoom"
)

// ZoomOAuthConfig holds decrypted Zoom OAuth app settings loaded from the DB.
// Used by server.go to build the initial zoom client on startup.
type ZoomOAuthConfig struct {
	ClientID     string
	ClientSecret string
}

// LoadZoomSettingsFromDB reads the Zoom OAuth app credentials from server_settings and
// decrypts the client secret. Returns nil (not an error) when client_id is empty.
func LoadZoomSettingsFromDB(db *sql.DB, encKey [32]byte) (*ZoomOAuthConfig, error) {
	var clientID, secretEnc string
	err := db.QueryRow(`
		SELECT zoom_client_id, zoom_client_secret_enc
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
			return nil, fmt.Errorf("decrypt zoom client secret: %w", err)
		}
	}
	return &ZoomOAuthConfig{ClientID: clientID, ClientSecret: clientSecret}, nil
}

// zoomRedirectURI is where Zoom returns the per-host OAuth code.
func (h *Handler) zoomRedirectURI() string { return h.baseURL + "/v1/zoom/callback" }

// GetZoomSettings handles GET /v1/settings/zoom (admin only).
func (h *Handler) GetZoomSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var clientID, secretEnc string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT zoom_client_id, zoom_client_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "zoom settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"client_id":         clientID,
		"client_secret_set": secretEnc != "",
		"configured":        clientID != "",
		// The exact Redirect URL + scopes to register in the Zoom Marketplace app, so
		// the setup UI shows values that always match what we send.
		"redirect_uri": h.zoomRedirectURI(),
	})
}

// PatchZoomSettings handles PATCH /v1/settings/zoom (admin only). Saves the Zoom OAuth app
// credentials and hot-reloads the zoom client so changes take effect without a restart.
// An empty client_secret keeps the existing stored secret.
func (h *Handler) PatchZoomSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
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

	if req.ClientID == "" {
		// Clearing credentials — wipe both columns and disable Zoom minting.
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  zoom_client_id = '', zoom_client_secret_enc = '',
			  updated_at = datetime('now')
			WHERE id = 1`); err != nil {
			h.logger.ErrorContext(r.Context(), "zoom settings: clear", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.SetZoom(nil)
		h.GetZoomSettings(w, r)
		return
	}

	if req.ClientSecret != "" {
		enc, err := secret.Encrypt(h.encKey, req.ClientSecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "zoom settings: encrypt secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err = h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  zoom_client_id = ?, zoom_client_secret_enc = ?,
			  updated_at = datetime('now')
			WHERE id = 1`, req.ClientID, enc); err != nil {
			h.logger.ErrorContext(r.Context(), "zoom settings: update", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  zoom_client_id = ?,
			  updated_at = datetime('now')
			WHERE id = 1`, req.ClientID); err != nil {
			h.logger.ErrorContext(r.Context(), "zoom settings: update (keep secret)", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Hot-reload: resolve the active secret and rebuild the zoom client.
	resolvedSecret := req.ClientSecret
	if resolvedSecret == "" {
		var secretEnc string
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT zoom_client_secret_enc FROM server_settings WHERE id = 1`).
			Scan(&secretEnc); err != nil {
			h.logger.ErrorContext(r.Context(), "zoom settings: re-read secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if secretEnc != "" {
			var decErr error
			resolvedSecret, decErr = secret.Decrypt(h.encKey, secretEnc)
			if decErr != nil {
				h.logger.ErrorContext(r.Context(), "zoom settings: decrypt existing secret", "error", decErr)
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}

	if resolvedSecret != "" {
		encKeyHex := hex.EncodeToString(h.encKey[:])
		zc, err := zoom.New(h.db, req.ClientID, resolvedSecret, h.zoomRedirectURI(), encKeyHex)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "zoom settings: hot-reload failed", "error", err)
			h.writeError(w, http.StatusInternalServerError, "failed to initialize Zoom client")
			return
		}
		h.SetZoom(zc)
		h.logger.Info("zoom settings: credentials updated and client hot-reloaded")
	}

	h.GetZoomSettings(w, r)
}
