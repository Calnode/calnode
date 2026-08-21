package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"
)

// SMTPConfig holds decrypted email settings loaded from the DB.
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

	// ResendAPIKey, when set, switches delivery to Resend's HTTPS API instead of SMTP.
	ResendAPIKey string
}

// EmailTransport names the delivery path in use. Surfaced to the admin UI because
// "SMTP settings are filled in" and "mail is going out over SMTP" can now differ.
type EmailTransport string

const (
	TransportNone   EmailTransport = "none"
	TransportResend EmailTransport = "resend_api"
	TransportSMTP   EmailTransport = "smtp"
)

// BuildMailer picks the delivery transport for a config, and is the single place that
// decision is made - both boot and the settings-save path call it, so they cannot drift.
//
// The rule is deliberately dumb: an API key means the admin wants the HTTPS API. It is
// NOT probe-and-fallback. A startup probe would test reachability at boot rather than at
// send time, a reachable TCP port is not the same thing as a working delivery path, and
// silently switching transports makes "which path sent this message?" unanswerable while
// masking a genuinely broken SMTP config. Explicit credentials say what the admin meant.
//
// An API key with no from address still selects Resend rather than quietly falling back to
// SMTP: the provider then returns a specific complaint the test button can show, which
// beats ignoring a key the admin deliberately pasted in.
func BuildMailer(cfg SMTPConfig) (mailer.Mailer, EmailTransport) {
	switch {
	case cfg.ResendAPIKey != "":
		return mailer.NewResend(cfg.ResendAPIKey, cfg.From, cfg.FromName), TransportResend
	case cfg.Host != "":
		return mailer.NewSMTP(cfg.Host, cfg.Port, cfg.User, cfg.Pass,
			cfg.TLS, cfg.StartTLS, cfg.From, cfg.FromName), TransportSMTP
	default:
		return &mailer.Noop{}, TransportNone
	}
}

// LoadEmailSettingsFromDB reads SMTP settings from server_settings and decrypts
// the password. Returns nil (not an error) when smtp_host is empty — meaning
// the settings have not been configured yet.
func LoadEmailSettingsFromDB(db *sql.DB, encKey [32]byte) (*SMTPConfig, error) {
	var host, port, user, passEnc, from, fromName, resendEnc string
	var smtpTLS, startTLS int
	err := db.QueryRow(`
		SELECT smtp_host, smtp_port, smtp_user, smtp_pass_enc,
		       smtp_tls, smtp_starttls, email_from, email_from_name,
		       resend_api_key_enc
		FROM server_settings WHERE id = 1`).
		Scan(&host, &port, &user, &passEnc, &smtpTLS, &startTLS, &from, &fromName, &resendEnc)
	// An API key alone is a complete configuration, so "no smtp_host" no longer means
	// "email is unconfigured" - checking only the host here would leave an API-key-only
	// install silently unable to send.
	if err == sql.ErrNoRows || (host == "" && resendEnc == "") {
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
	var resendKey string
	if resendEnc != "" {
		resendKey, err = secret.Decrypt(encKey, resendEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt resend api key: %w", err)
		}
	}
	return &SMTPConfig{
		Host: host, Port: port, User: user, Pass: pass,
		TLS: smtpTLS != 0, StartTLS: startTLS != 0,
		From: from, FromName: fromName,
		ResendAPIKey: resendKey,
	}, nil
}

// GetEmailSettings handles GET /v1/settings/email.
// Returns current SMTP configuration. The password is never returned;
// smtp_pass_set indicates whether one is stored.
func (h *Handler) GetEmailSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var host, port, user, passEnc, from, fromName, resendEnc string
	var smtpTLS, startTLS int
	err := h.db.QueryRowContext(r.Context(), `
		SELECT smtp_host, smtp_port, smtp_user, smtp_pass_enc,
		       smtp_tls, smtp_starttls, email_from, email_from_name,
		       resend_api_key_enc
		FROM server_settings WHERE id = 1`).
		Scan(&host, &port, &user, &passEnc, &smtpTLS, &startTLS, &from, &fromName, &resendEnc)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "email settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Report the transport the same way BuildMailer decides it, so the UI cannot claim one
	// path while another is delivering. Secrets are never returned, only whether they exist.
	_, transport := BuildMailer(SMTPConfig{Host: host, ResendAPIKey: resendEnc})
	h.writeJSON(w, http.StatusOK, map[string]any{
		"smtp_host":          host,
		"smtp_port":          port,
		"smtp_user":          user,
		"smtp_pass_set":      passEnc != "",
		"smtp_tls":           smtpTLS != 0,
		"smtp_starttls":      startTLS != 0,
		"email_from":         from,
		"email_from_name":    fromName,
		"resend_api_key_set": resendEnc != "",
		"transport":          string(transport),
		"enabled":            h.isEmailEnabled(),
	})
}

// emailSecret identifies which encrypted settings column to write. A closed type rather
// than a column-name string: the SQL below is then fully static, so there is no formatted
// query for gosec to flag (G201) and no suppression comment asserting safety that a later
// edit could quietly invalidate. Two columns do not justify dynamic SQL.
type emailSecret int

const (
	secretSMTPPass emailSecret = iota
	secretResendAPIKey
)

func (s emailSecret) String() string {
	if s == secretResendAPIKey {
		return "resend_api_key_enc"
	}
	return "smtp_pass_enc"
}

// execer is the subset of *sql.DB / *sql.Tx the settings writes need, so they can be run
// inside a transaction. Saving email settings touches up to three columns across separate
// statements; without a transaction a failure partway leaves the instance holding, say, a
// new SMTP host with the previous password.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// storeEmailSecret encrypts and stores one secret, or clears it when value is empty.
func (h *Handler) storeEmailSecret(ctx context.Context, db execer, which emailSecret, value string) error {
	enc := ""
	if value != "" {
		var err error
		enc, err = secret.Encrypt(h.encKey, value)
		if err != nil {
			return fmt.Errorf("encrypt %s: %w", which, err)
		}
	}

	var q string
	switch which {
	case secretResendAPIKey:
		q = `UPDATE server_settings SET resend_api_key_enc = ?, updated_at = datetime('now') WHERE id = 1`
	case secretSMTPPass:
		q = `UPDATE server_settings SET smtp_pass_enc = ?, updated_at = datetime('now') WHERE id = 1`
	default:
		return fmt.Errorf("unknown email secret %d", int(which))
	}

	if _, err := db.ExecContext(ctx, q, enc); err != nil {
		return fmt.Errorf("store %s: %w", which, err)
	}
	return nil
}

// PatchEmailSettings handles PATCH /v1/settings/email.
// Admin-only. Saves SMTP settings to the DB and hot-swaps the live mailer.
// If smtp_pass is omitted or empty, the existing stored password is kept.
func (h *Handler) PatchEmailSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
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
		// Optional; omit to keep the existing key, send "" explicitly to clear it and
		// fall back to SMTP.
		ResendAPIKey *string `json:"resend_api_key"`
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

	// One transaction for all of it. Saving touches up to three statements (the non-secret
	// columns, then each supplied secret), and a failure partway through would otherwise
	// leave the instance holding a mismatched pair - a new SMTP host with the old password,
	// or an API key without the From address it needs.
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "email settings: begin", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if _, err := tx.ExecContext(r.Context(), `
		UPDATE server_settings SET
		  smtp_host = ?, smtp_port = ?, smtp_user = ?,
		  smtp_tls = ?, smtp_starttls = ?,
		  email_from = ?, email_from_name = ?,
		  updated_at = datetime('now')
		WHERE id = 1`,
		req.SMTPHost, req.SMTPPort, req.SMTPUser,
		boolToInt(req.SMTPTLS), boolToInt(req.SMTPStartTLS),
		req.EmailFrom, req.EmailFromName); err != nil {
		h.logger.ErrorContext(r.Context(), "email settings: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if req.SMTPPass != "" {
		if err := h.storeEmailSecret(r.Context(), tx, secretSMTPPass, req.SMTPPass); err != nil {
			h.logger.ErrorContext(r.Context(), "email settings: smtp password", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	// A pointer, so "field omitted" (keep) is distinguishable from "" (clear). Without that
	// an admin could never turn the API key back off and return to SMTP.
	if req.ResendAPIKey != nil {
		if err := h.storeEmailSecret(r.Context(), tx, secretResendAPIKey, *req.ResendAPIKey); err != nil {
			h.logger.ErrorContext(r.Context(), "email settings: resend key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "email settings: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Hot-swap the live mailer so changes take effect immediately. Re-read from the DB
	// rather than rebuilding from the request: the request may legitimately omit secrets,
	// and this way the running mailer always matches what was actually persisted.
	if h.live != nil {
		cfg, err := LoadEmailSettingsFromDB(h.db, h.encKey)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "email settings: reload after save", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if cfg == nil {
			h.live.Swap(&mailer.Noop{})
		} else {
			m, transport := BuildMailer(*cfg)
			h.live.Swap(m)
			h.logger.InfoContext(r.Context(), "mailer: reconfigured", "transport", string(transport))
		}
	}

	h.GetEmailSettings(w, r)
}

// testEmailTimeout bounds the interactive test-send. Short on purpose: see the call site.
const testEmailTimeout = 12 * time.Second

// testEmailErrorMessage turns a send failure into something an admin can act on.
//
// It takes the active transport because ErrUnreachable is raised by BOTH transports and the
// right advice is opposite in each case. On SMTP, "could not connect" usually means the
// platform is blocking the port and the fix is to switch to the HTTPS API. On the HTTPS
// path, telling an admin to add a Resend API key is nonsense - they already have one, and
// the cause is an ordinary network or provider outage.
func testEmailErrorMessage(err error, transport EmailTransport) string {
	switch {
	case errors.Is(err, mailer.ErrEmailRejected):
		// The provider understood the request and refused it. Its own explanation is far
		// more useful than anything we could infer (unverified domain, revoked key, bad
		// address), so lead with that.
		return "Resend rejected the message: " + err.Error()

	case errors.Is(err, mailer.ErrUnreachable) && transport == TransportResend:
		return "Could not reach api.resend.com. Delivery is set to the Resend API over " +
			"HTTPS, so this is a network or provider problem rather than a settings one. " +
			"Check that outbound HTTPS is allowed and try again."

	case errors.Is(err, mailer.ErrUnreachable):
		return "Could not reach the SMTP server: the connection timed out or was refused. " +
			"Many hosting platforms block outbound SMTP on their cheaper plans, which looks " +
			"exactly like this even when your username and password are correct. If you use " +
			"Resend, add an API key under Settings → Email to send over HTTPS instead, which " +
			"is not blocked. Otherwise check the host, port and TLS mode."

	case errors.Is(err, context.DeadlineExceeded) && transport == TransportResend:
		return "Timed out talking to the Resend API. Check outbound HTTPS and try again."

	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out sending the test email. The SMTP server accepted a connection but " +
			"did not finish the exchange in time. Check the port and TLS mode (465 uses " +
			"implicit TLS; 587 uses STARTTLS)."

	default:
		return "Failed to send test email: " + err.Error()
	}
}

// TestEmailConnection handles POST /v1/settings/email/test.
// Admin-only. Sends a plain connection-check email to the authenticated user's address.
func (h *Handler) TestEmailConnection(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
		return
	}

	if !h.isEmailEnabled() {
		h.writeError(w, http.StatusServiceUnavailable,
			"Email is not configured — save SMTP settings first")
		return
	}
	// Bound this well under the default send timeout. This endpoint is interactive: an
	// admin is watching a spinner, and a long wait reads as "broken UI" rather than
	// "email is misconfigured". Background sends keep the longer default.
	ctx, cancel := context.WithTimeout(r.Context(), testEmailTimeout)
	defer cancel()

	// Resolve the active transport so a failure can be explained in terms of the path
	// actually in use. Best-effort: if this read fails the send is still attempted, and the
	// message just falls back to the SMTP-flavoured advice.
	transport := TransportSMTP
	if cfg, cfgErr := LoadEmailSettingsFromDB(h.db, h.encKey); cfgErr == nil && cfg != nil {
		_, transport = BuildMailer(*cfg)
	}

	if err := h.mailer.Send(ctx, mailer.Message{
		To:      []string{user.Email},
		Subject: "[TEST] Calnode email configuration",
		Text:    "This is a test email from Calnode. If you received this, your email settings are working correctly.",
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "email connection test: send",
			"transport", string(transport), "error", err)
		h.writeError(w, http.StatusInternalServerError, testEmailErrorMessage(err, transport))
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": user.Email})
}
