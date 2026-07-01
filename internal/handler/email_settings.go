package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"
)

// SMTPConfig holds decrypted SMTP settings loaded from the DB.
// Used by server.go to build the initial mailer on startup.
type SMTPConfig struct {
	Host     string
	Port     string
	User     string
	Pass     string
	TLS      bool
	StartTLS bool
	From     string
	FromName string
}

// LoadEmailSettingsFromDB reads SMTP settings from server_settings and decrypts
// the password. Returns nil (not an error) when smtp_host is empty — meaning
// the settings have not been configured yet.
func LoadEmailSettingsFromDB(db *sql.DB, encKey [32]byte) (*SMTPConfig, error) {
	var host, port, user, passEnc, from, fromName string
	var smtpTLS, startTLS int
	err := db.QueryRow(`
		SELECT smtp_host, smtp_port, smtp_user, smtp_pass_enc,
		       smtp_tls, smtp_starttls, email_from, email_from_name
		FROM server_settings WHERE id = 1`).
		Scan(&host, &port, &user, &passEnc, &smtpTLS, &startTLS, &from, &fromName)
	if err == sql.ErrNoRows || host == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pass string
	if passEnc != "" {
		pass, err = secret.Decrypt(encKey, passEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt smtp password: %w", err)
		}
	}
	return &SMTPConfig{
		Host: host, Port: port, User: user, Pass: pass,
		TLS: smtpTLS != 0, StartTLS: startTLS != 0,
		From: from, FromName: fromName,
	}, nil
}

// GetEmailSettings handles GET /v1/settings/email.
// Returns current SMTP configuration. The password is never returned;
// smtp_pass_set indicates whether one is stored.
func (h *Handler) GetEmailSettings(w http.ResponseWriter, r *http.Request) {
	caller, ok := userFromContext(r.Context())
	if !ok || !caller.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	var host, port, user, passEnc, from, fromName string
	var smtpTLS, startTLS int
	err := h.db.QueryRowContext(r.Context(), `
		SELECT smtp_host, smtp_port, smtp_user, smtp_pass_enc,
		       smtp_tls, smtp_starttls, email_from, email_from_name
		FROM server_settings WHERE id = 1`).
		Scan(&host, &port, &user, &passEnc, &smtpTLS, &startTLS, &from, &fromName)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "email settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"smtp_host":       host,
		"smtp_port":       port,
		"smtp_user":       user,
		"smtp_pass_set":   passEnc != "",
		"smtp_tls":        smtpTLS != 0,
		"smtp_starttls":   startTLS != 0,
		"email_from":      from,
		"email_from_name": fromName,
		"enabled":         h.isEmailEnabled(),
	})
}

// PatchEmailSettings handles PATCH /v1/settings/email.
// Admin-only. Saves SMTP settings to the DB and hot-swaps the live mailer.
// If smtp_pass is omitted or empty, the existing stored password is kept.
func (h *Handler) PatchEmailSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		SMTPHost      string `json:"smtp_host"`
		SMTPPort      string `json:"smtp_port"`
		SMTPUser      string `json:"smtp_user"`
		SMTPPass      string `json:"smtp_pass"` // optional; omit to keep existing
		SMTPTLS       bool   `json:"smtp_tls"`
		SMTPStartTLS  bool   `json:"smtp_starttls"`
		EmailFrom     string `json:"email_from"`
		EmailFromName string `json:"email_from_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SMTPPort == "" {
		req.SMTPPort = "587"
	} else {
		p, err := strconv.Atoi(req.SMTPPort)
		if err != nil || p < 1 || p > 65535 {
			h.writeError(w, http.StatusBadRequest, "smtp_port must be a number between 1 and 65535")
			return
		}
	}
	if req.EmailFromName == "" {
		req.EmailFromName = "Calnode"
	}

	boolToInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}

	if req.SMTPPass != "" {
		enc, err := secret.Encrypt(h.encKey, req.SMTPPass)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "email settings: encrypt password", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		_, err = h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  smtp_host = ?, smtp_port = ?, smtp_user = ?, smtp_pass_enc = ?,
			  smtp_tls = ?, smtp_starttls = ?,
			  email_from = ?, email_from_name = ?,
			  updated_at = datetime('now')
			WHERE id = 1`,
			req.SMTPHost, req.SMTPPort, req.SMTPUser, enc,
			boolToInt(req.SMTPTLS), boolToInt(req.SMTPStartTLS),
			req.EmailFrom, req.EmailFromName)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "email settings: update", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		_, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  smtp_host = ?, smtp_port = ?, smtp_user = ?,
			  smtp_tls = ?, smtp_starttls = ?,
			  email_from = ?, email_from_name = ?,
			  updated_at = datetime('now')
			WHERE id = 1`,
			req.SMTPHost, req.SMTPPort, req.SMTPUser,
			boolToInt(req.SMTPTLS), boolToInt(req.SMTPStartTLS),
			req.EmailFrom, req.EmailFromName)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "email settings: update (keep pass)", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Hot-swap the live mailer so changes take effect immediately.
	if h.live != nil {
		if req.SMTPHost != "" {
			// Resolve the password: use what the caller sent, or fetch the stored one.
			pass := req.SMTPPass
			if pass == "" {
				var enc string
				if err := h.db.QueryRowContext(r.Context(),
					`SELECT smtp_pass_enc FROM server_settings WHERE id = 1`).Scan(&enc); err != nil {
					h.logger.WarnContext(r.Context(), "email settings: re-fetch enc pass failed, using empty pass", "error", err)
				} else if enc != "" {
					var decErr error
					pass, decErr = secret.Decrypt(h.encKey, enc)
					if decErr != nil {
						h.logger.WarnContext(r.Context(), "email settings: decrypt stored pass failed, using empty pass", "error", decErr)
					}
				}
			}
			h.live.Swap(mailer.NewSMTP(
				req.SMTPHost, req.SMTPPort, req.SMTPUser, pass,
				req.SMTPTLS, req.SMTPStartTLS, req.EmailFrom, req.EmailFromName,
			))
		} else {
			h.live.Swap(&mailer.Noop{})
		}
	}

	h.GetEmailSettings(w, r)
}

// TestEmailConnection handles POST /v1/settings/email/test.
// Admin-only. Sends a plain connection-check email to the authenticated user's address.
func (h *Handler) TestEmailConnection(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}

	if !h.isEmailEnabled() {
		h.writeError(w, http.StatusServiceUnavailable,
			"Email is not configured — save SMTP settings first")
		return
	}
	if err := h.mailer.Send(r.Context(), mailer.Message{
		To:      []string{user.Email},
		Subject: "[TEST] Calnode email configuration",
		Text:    "This is a test email from Calnode. If you received this, your SMTP settings are working correctly.",
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "email connection test: send", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to send test email")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": user.Email})
}
