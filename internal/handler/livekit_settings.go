package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"encoding/json"

	"github.com/calnode/calnode/internal/livekit"
	"github.com/calnode/calnode/internal/secret"
)

// LiveKitConfig holds decrypted LiveKit server settings loaded from the DB.
type LiveKitConfig struct {
	URL       string
	APIKey    string
	APISecret string
}

// LoadLiveKitSettingsFromDB reads the LiveKit server config from server_settings and decrypts
// the API secret. Returns nil (not an error) when the URL or key is empty (= not configured).
func LoadLiveKitSettingsFromDB(db *sql.DB, encKey [32]byte) (*LiveKitConfig, error) {
	var url, apiKey, secretEnc string
	err := db.QueryRow(`
		SELECT livekit_url, livekit_api_key, livekit_api_secret_enc
		FROM server_settings WHERE id = 1`).Scan(&url, &apiKey, &secretEnc)
	if err == sql.ErrNoRows || url == "" || apiKey == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var apiSecret string
	if secretEnc != "" {
		apiSecret, err = secret.Decrypt(encKey, secretEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt livekit api secret: %w", err)
		}
	}
	return &LiveKitConfig{URL: url, APIKey: apiKey, APISecret: apiSecret}, nil
}

// GetLiveKitSettings handles GET /v1/settings/livekit (admin only).
func (h *Handler) GetLiveKitSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var url, apiKey, secretEnc string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT livekit_url, livekit_api_key, livekit_api_secret_enc
		FROM server_settings WHERE id = 1`).Scan(&url, &apiKey, &secretEnc)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "livekit settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"url":            url,
		"api_key":        apiKey,
		"api_secret_set": secretEnc != "",
		"configured":     url != "" && apiKey != "" && secretEnc != "",
	})
}

// PatchLiveKitSettings handles PATCH /v1/settings/livekit (admin only). Stores the config and
// hot-reloads the client. An empty url clears LiveKit; an empty api_secret keeps the stored one.
func (h *Handler) PatchLiveKitSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		URL       string `json:"url"`
		APIKey    string `json:"api_key"`
		APISecret string `json:"api_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.APIKey = strings.TrimSpace(req.APIKey)

	if req.URL == "" {
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET livekit_url = '', livekit_api_key = '',
			  livekit_api_secret_enc = '', updated_at = datetime('now') WHERE id = 1`); err != nil {
			h.logger.ErrorContext(r.Context(), "livekit settings: clear", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.SetLiveKit(nil)
		h.GetLiveKitSettings(w, r)
		return
	}
	// URL sanity + best-effort SSRF check. The browser SDK needs a ws(s)/http(s) origin;
	// self-hosted LiveKit on a private network is a legitimate, intended configuration
	// (same reasoning as CalDAV/BYO-LLM), so only the cloud-metadata range is blocked —
	// see validateBYOServerURL (shared with the CalDAV and BYO-LLM URL checks). The
	// livekit.Client's own http.Client re-checks at dial time either way.
	if err := validateBYOServerURL(r.Context(), req.URL, "server URL", "ws", "wss", "http", "https"); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.APISecret != "" {
		enc, err := secret.Encrypt(h.encKey, req.APISecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "livekit settings: encrypt secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err = h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET livekit_url = ?, livekit_api_key = ?,
			  livekit_api_secret_enc = ?, updated_at = datetime('now') WHERE id = 1`,
			req.URL, req.APIKey, enc); err != nil {
			h.logger.ErrorContext(r.Context(), "livekit settings: update", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET livekit_url = ?, livekit_api_key = ?,
		  updated_at = datetime('now') WHERE id = 1`, req.URL, req.APIKey); err != nil {
		h.logger.ErrorContext(r.Context(), "livekit settings: update (keep secret)", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Resolve the active secret and hot-reload the client.
	resolvedSecret := req.APISecret
	if resolvedSecret == "" {
		var secretEnc string
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT livekit_api_secret_enc FROM server_settings WHERE id = 1`).Scan(&secretEnc); err != nil {
			h.logger.ErrorContext(r.Context(), "livekit settings: re-read secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if secretEnc != "" {
			var decErr error
			if resolvedSecret, decErr = secret.Decrypt(h.encKey, secretEnc); decErr != nil {
				h.logger.ErrorContext(r.Context(), "livekit settings: decrypt existing secret", "error", decErr)
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}
	if resolvedSecret != "" {
		h.SetLiveKit(livekit.New(req.URL, req.APIKey, resolvedSecret, h.encKey))
		h.logger.Info("livekit settings: updated and client hot-reloaded")
	}

	h.GetLiveKitSettings(w, r)
}
