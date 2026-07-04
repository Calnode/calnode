package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/stripe"
)

// StripeConfig holds decrypted Stripe settings loaded from the DB.
type StripeConfig struct {
	SecretKey      string
	PublishableKey string
	WebhookSecret  string
}

// LoadStripeSettingsFromDB reads Stripe credentials from server_settings and decrypts the
// secret key + webhook secret. Returns nil (not an error) when the secret key is unset.
func LoadStripeSettingsFromDB(db *sql.DB, encKey [32]byte) (*StripeConfig, error) {
	var secretEnc, pubKey, whEnc string
	err := db.QueryRow(`
		SELECT stripe_secret_key_enc, stripe_publishable_key, stripe_webhook_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&secretEnc, &pubKey, &whEnc)
	if err == sql.ErrNoRows || secretEnc == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := &StripeConfig{PublishableKey: pubKey}
	if cfg.SecretKey, err = secret.Decrypt(encKey, secretEnc); err != nil {
		return nil, fmt.Errorf("decrypt stripe secret key: %w", err)
	}
	if whEnc != "" {
		if cfg.WebhookSecret, err = secret.Decrypt(encKey, whEnc); err != nil {
			return nil, fmt.Errorf("decrypt stripe webhook secret: %w", err)
		}
	}
	return cfg, nil
}

// stripeWebhookURL is the endpoint to register in the Stripe dashboard.
func (h *Handler) stripeWebhookURL() string { return h.baseURL + "/v1/stripe/webhook" }

// GetStripeSettings handles GET /v1/settings/stripe (admin only).
func (h *Handler) GetStripeSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var secretEnc, pubKey, whEnc string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT stripe_secret_key_enc, stripe_publishable_key, stripe_webhook_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&secretEnc, &pubKey, &whEnc)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "stripe settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"publishable_key":    pubKey,
		"secret_key_set":     secretEnc != "",
		"webhook_secret_set": whEnc != "",
		// configured = can take a payment AND verify the confirming webhook.
		"configured":  secretEnc != "" && whEnc != "",
		"webhook_url": h.stripeWebhookURL(),
	})
}

// PatchStripeSettings handles PATCH /v1/settings/stripe (admin only). Saves credentials and
// hot-reloads the Stripe client. Blank secret_key/webhook_secret keep the stored values.
func (h *Handler) PatchStripeSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		SecretKey      string  `json:"secret_key"`
		PublishableKey *string `json:"publishable_key"`
		WebhookSecret  string  `json:"webhook_secret"`
		Clear          bool    `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Clear {
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  stripe_secret_key_enc = '', stripe_publishable_key = '', stripe_webhook_secret_enc = '',
			  updated_at = datetime('now')
			WHERE id = 1`); err != nil {
			h.logger.ErrorContext(r.Context(), "stripe settings: clear", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.SetStripe(nil)
		h.GetStripeSettings(w, r)
		return
	}

	// Update publishable key (plain — it's a public value) when provided.
	if req.PublishableKey != nil {
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE server_settings SET stripe_publishable_key = ?, updated_at = datetime('now') WHERE id = 1`,
			*req.PublishableKey); err != nil {
			h.logger.ErrorContext(r.Context(), "stripe settings: update pub key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	// Encrypt + store secret key / webhook secret only when a new non-blank value is given.
	if req.SecretKey != "" {
		enc, err := secret.Encrypt(h.encKey, req.SecretKey)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "stripe settings: encrypt secret key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE server_settings SET stripe_secret_key_enc = ?, updated_at = datetime('now') WHERE id = 1`, enc); err != nil {
			h.logger.ErrorContext(r.Context(), "stripe settings: update secret key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if req.WebhookSecret != "" {
		enc, err := secret.Encrypt(h.encKey, req.WebhookSecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "stripe settings: encrypt webhook secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE server_settings SET stripe_webhook_secret_enc = ?, updated_at = datetime('now') WHERE id = 1`, enc); err != nil {
			h.logger.ErrorContext(r.Context(), "stripe settings: update webhook secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Hot-reload from the now-current DB state.
	cfg, err := LoadStripeSettingsFromDB(h.db, h.encKey)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "stripe settings: reload", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cfg == nil {
		h.SetStripe(nil)
	} else if sc, err := stripe.New(cfg.SecretKey, cfg.PublishableKey, cfg.WebhookSecret); err != nil {
		h.logger.ErrorContext(r.Context(), "stripe settings: init client", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to initialize Stripe client")
		return
	} else {
		h.SetStripe(sc)
		h.logger.Info("stripe settings: credentials updated and client hot-reloaded")
	}

	h.GetStripeSettings(w, r)
}
