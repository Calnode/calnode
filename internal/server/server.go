package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/calnode/calnode/frontend"
	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

// New builds the HTTP mux and starts background services. It returns the
// handler and a drain function that blocks until the background worker has
// finished its current poll cycle — call drain before httpServer.Shutdown.
func New(ctx context.Context, cfg *config.Config, db *sql.DB, logger *slog.Logger) (http.Handler, func()) {
	mux := http.NewServeMux()
	h := handler.New(db, logger)
	h.SetBaseURL(cfg.BaseURL)
	h.SetPublicBaseURL(cfg.PublicBaseURL)
	h.SetDataDir("data")
	h.SetEncKey(cfg.EncryptionKey)

	encKey, _ := secret.ParseKey(cfg.EncryptionKey)

	// Create the hot-swappable mailer wrapper. All sends everywhere use this
	// reference, so changing SMTP settings in the UI takes effect immediately.
	live := mailer.NewLive(&mailer.Noop{})

	// DB settings take priority over env vars — they're what the UI controls.
	dbSMTP, dbErr := handler.LoadEmailSettingsFromDB(db, encKey)
	if dbErr != nil {
		logger.Warn("mailer: could not load settings from database", "error", dbErr)
	}

	switch {
	case dbSMTP != nil && dbSMTP.Host != "":
		live.Swap(mailer.NewSMTP(
			dbSMTP.Host, dbSMTP.Port, dbSMTP.User, dbSMTP.Pass,
			dbSMTP.TLS, dbSMTP.StartTLS, dbSMTP.From, dbSMTP.FromName,
		))
		logger.Info("mailer: configured from database", "host", dbSMTP.Host, "port", dbSMTP.Port)

	case cfg.SMTPHost != "":
		live.Swap(mailer.NewSMTP(
			cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass,
			cfg.SMTPTLS, cfg.SMTPStartTLS, cfg.EmailFrom, cfg.EmailFromName,
		))
		logger.Info("mailer: configured from environment", "host", cfg.SMTPHost, "port", cfg.SMTPPort)
		// Seed env-var settings into DB so they appear in the UI on first boot.
		seedSMTPToDB(db, cfg, encKey, logger)

	default:
		logger.Info("mailer: not configured — configure SMTP in Settings or set EMAIL_SMTP_HOST")
	}

	h.SetMailer(live, cfg.BaseURL)

	drain := func() {}
	whs, err := webhook.New(db, cfg.EncryptionKey)
	if err != nil {
		logger.Error("webhook: init failed", "error", err)
	} else {
		h.SetWebhookSvc(whs)
		// Pass live so the worker picks up SMTP changes automatically.
		wrk := worker.New(db, whs, logger, worker.WithMailer(live))
		go wrk.Run(ctx)
		drain = wrk.Wait
		logger.Info("webhook worker started")
	}

	// DB Google settings take priority over env vars.
	googleClientID := cfg.GoogleClientID
	googleClientSecret := cfg.GoogleClientSecret
	if dbGoogle, dbGoogleErr := handler.LoadGoogleSettingsFromDB(db, encKey); dbGoogleErr != nil {
		logger.Warn("google settings: could not load from database", "error", dbGoogleErr)
	} else if dbGoogle != nil {
		googleClientID = dbGoogle.ClientID
		googleClientSecret = dbGoogle.ClientSecret
		logger.Info("Google OAuth: credentials loaded from database")
	}

	if googleClientID != "" {
		authRedirect := cfg.BaseURL + "/v1/auth/callback"
		h.SetGoogleAuth(googleClientID, googleClientSecret, authRedirect, cfg.CookieSecure)
		logger.Info("Google OAuth login configured", "redirect_url", authRedirect)

		calRedirect := cfg.BaseURL + "/v1/calendar/callback"
		gc, err := gcal.New(db, googleClientID, googleClientSecret, calRedirect, cfg.EncryptionKey)
		if err != nil {
			logger.Error("gcal: init failed", "error", err)
		} else {
			h.SetCalendar(gc)
			logger.Info("Google Calendar configured", "redirect_url", calRedirect)
		}
	} else {
		logger.Info("Google OAuth not configured — add credentials in Settings or set GOOGLE_CLIENT_ID")
	}

	// Ops
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)
	mux.HandleFunc("GET /version", h.Version)

	// Bootstrap — public, once-only
	mux.HandleFunc("POST /v1/setup", h.Setup)

	// Auth status (public — drives login page rendering).
	mux.HandleFunc("GET /v1/auth/status", h.AuthStatus)

	// First-user claim (public — only succeeds when no users exist).
	claimRL := RateLimit(5, time.Minute)
	mux.HandleFunc("POST /v1/auth/claim", claimRL(h.Claim))

	// Email + password login.
	loginRL := RateLimit(10, time.Minute)
	mux.HandleFunc("POST /v1/auth/login/email", loginRL(h.LoginEmail))

	// Google OAuth login (browser sessions for admin UI).
	authRL := RateLimit(10, time.Minute)
	mux.HandleFunc("GET /v1/auth/login", authRL(h.LoginGoogle))
	mux.HandleFunc("GET /v1/auth/callback", authRL(h.CallbackGoogle))
	mux.HandleFunc("POST /v1/auth/logout", h.Logout)

	// Password management.
	mux.HandleFunc("POST /v1/users/me/password", h.RequireAuth(h.ChangePassword))
	mux.HandleFunc("POST /v1/users/{id}/password", h.RequireAuth(h.AdminSetPassword))

	// Invite management.
	inviteRL := RateLimit(20, time.Minute)
	mux.HandleFunc("POST /v1/invites", inviteRL(h.RequireAuth(h.CreateInvite)))
	mux.HandleFunc("GET /v1/invites", h.RequireAuth(h.ListInvites))
	mux.HandleFunc("DELETE /v1/invites/{id}", h.RequireAuth(h.RevokeInvite))
	mux.HandleFunc("GET /v1/invites/{token}", h.GetInvite)
	mux.HandleFunc("POST /v1/invites/{token}/claim", inviteRL(h.ClaimInvite))

	// Users
	mux.HandleFunc("GET /v1/users", h.RequireAuth(h.ListUsers))
	mux.HandleFunc("DELETE /v1/users/{id}", h.RequireAuth(h.DeleteUser))
	mux.HandleFunc("PATCH /v1/users/{id}/role", h.RequireAuth(h.SetUserRole))
	mux.HandleFunc("POST /v1/users/{id}/transfer-ownership", h.RequireAuth(h.TransferOwnership))
	mux.HandleFunc("POST /v1/users/{id}/archive", h.RequireAuth(h.ArchiveUser))
	mux.HandleFunc("POST /v1/users/{id}/restore", h.RequireAuth(h.RestoreUser))
	mux.HandleFunc("GET /v1/users/{id}/upcoming-bookings", h.RequireAuth(h.ListUserUpcomingBookings))

	// Teams
	mux.HandleFunc("POST /v1/teams", h.RequireAuth(h.CreateTeam))
	mux.HandleFunc("GET /v1/teams", h.RequireAuth(h.ListTeams))
	mux.HandleFunc("GET /v1/teams/{id}", h.RequireAuth(h.GetTeam))
	mux.HandleFunc("PATCH /v1/teams/{id}", h.RequireAuth(h.PatchTeam))
	mux.HandleFunc("DELETE /v1/teams/{id}", h.RequireAuth(h.DeleteTeam))
	mux.HandleFunc("POST /v1/teams/{id}/members", h.RequireAuth(h.AddTeamMember))
	mux.HandleFunc("PATCH /v1/teams/{id}/members/{userId}", h.RequireAuth(h.UpdateTeamMember))
	mux.HandleFunc("DELETE /v1/teams/{id}/members/{userId}", h.RequireAuth(h.RemoveTeamMember))
	mux.HandleFunc("GET /v1/users/me", h.RequireAuth(h.GetMe))
	mux.HandleFunc("PATCH /v1/users/me", h.RequireAuth(h.PatchMe))
	avatarRL := RateLimit(20, time.Minute)
	mux.HandleFunc("POST /v1/users/me/avatar", avatarRL(h.RequireAuth(h.UploadAvatar)))
	mux.HandleFunc("DELETE /v1/users/me/avatar", avatarRL(h.RequireAuth(h.DeleteAvatar)))
	mux.HandleFunc("GET /avatars/{userID}", h.ServeAvatar)

	// Server settings — email (SMTP) and Google OAuth
	settingsRL := RateLimit(20, time.Minute)
	mux.HandleFunc("GET /v1/settings/email", h.RequireAuth(h.GetEmailSettings))
	mux.HandleFunc("PATCH /v1/settings/email", settingsRL(h.RequireAuth(h.PatchEmailSettings)))
	mux.HandleFunc("POST /v1/settings/email/test", settingsRL(h.RequireAuth(h.TestEmailConnection)))
	mux.HandleFunc("GET /v1/settings/google", h.RequireAuth(h.GetGoogleSettings))
	mux.HandleFunc("PATCH /v1/settings/google", settingsRL(h.RequireAuth(h.PatchGoogleSettings)))

	// Event types
	mux.HandleFunc("POST /v1/event-types", h.RequireAuth(h.CreateEventType))
	mux.HandleFunc("GET /v1/event-types", h.RequireAuth(h.ListEventTypes))
	mux.HandleFunc("GET /v1/event-types/{slug}", h.RequireAuth(h.GetEventType))
	mux.HandleFunc("PATCH /v1/event-types/{slug}", h.RequireAuth(h.PatchEventType))
	mux.HandleFunc("DELETE /v1/event-types/{slug}", h.RequireAuth(h.DeleteEventType))
	mux.HandleFunc("GET /v1/event-types/{slug}/hosts", h.RequireAuth(h.ListEventTypeHosts))
	mux.HandleFunc("PUT /v1/event-types/{slug}/hosts", h.RequireAuth(h.SetEventTypeHosts))
	testEmailRL := RateLimit(10, time.Minute)
	mux.HandleFunc("POST /v1/event-types/{slug}/test-email", testEmailRL(h.RequireAuth(h.SendTestEmail)))

	// Availability rules
	mux.HandleFunc("POST /v1/availability-rules", h.RequireAuth(h.CreateAvailabilityRule))
	mux.HandleFunc("GET /v1/availability-rules", h.RequireAuth(h.ListAvailabilityRules))
	mux.HandleFunc("PATCH /v1/availability-rules/{id}", h.RequireAuth(h.UpdateAvailabilityRule))
	mux.HandleFunc("DELETE /v1/availability-rules/{id}", h.RequireAuth(h.DeleteAvailabilityRule))

	// Availability overrides
	mux.HandleFunc("POST /v1/availability-overrides", h.RequireAuth(h.CreateAvailabilityOverride))
	mux.HandleFunc("GET /v1/availability-overrides", h.RequireAuth(h.ListAvailabilityOverrides))
	mux.HandleFunc("PATCH /v1/availability-overrides/{id}", h.RequireAuth(h.UpdateAvailabilityOverride))
	mux.HandleFunc("DELETE /v1/availability-overrides/{id}", h.RequireAuth(h.DeleteAvailabilityOverride))

	// Slots — public
	mux.HandleFunc("GET /v1/event-types/{slug}/slots", h.GetSlots)

	// Intake questions
	mux.HandleFunc("GET /v1/event-types/{slug}/questions", h.ListQuestions)
	mux.HandleFunc("GET /v1/event-types/{slug}/questions/admin", h.RequireAuth(h.ListQuestionsAdmin))
	mux.HandleFunc("POST /v1/event-types/{slug}/questions", h.RequireAuth(h.CreateQuestion))
	mux.HandleFunc("PATCH /v1/event-types/{slug}/questions/{id}", h.RequireAuth(h.UpdateQuestion))
	mux.HandleFunc("DELETE /v1/event-types/{slug}/questions/{id}", h.RequireAuth(h.DeleteQuestion))

	bookingRL := RateLimit(20, time.Minute)
	manageRL := RateLimit(30, time.Minute)

	// Bookings
	mux.HandleFunc("POST /v1/bookings", bookingRL(h.CreateBooking))
	mux.HandleFunc("GET /v1/bookings/{id}", h.GetBooking)
	mux.HandleFunc("GET /v1/bookings", h.RequireAuth(h.ListBookings))
	mux.HandleFunc("POST /v1/bookings/{id}/cancel", h.RequireAuth(h.CancelBooking))
	mux.HandleFunc("PATCH /v1/bookings/{id}/reschedule", h.RequireAuth(h.RescheduleBooking))
	mux.HandleFunc("POST /v1/bookings/{id}/reassign", h.RequireAuth(h.ReassignBooking))
	mux.HandleFunc("GET /v1/bookings/{id}/answers", h.RequireAuth(h.GetBookingAnswers))

	// Public booking page
	mux.HandleFunc("GET /book/{slug}", h.BookPage)

	// Manage booking (reschedule / cancel via token link)
	mux.HandleFunc("GET /manage/{token}", manageRL(h.ManagePage))
	mux.HandleFunc("POST /manage/{token}/reschedule", h.RescheduleByToken)
	mux.HandleFunc("POST /manage/{token}/cancel", h.CancelByToken)

	// Webhooks
	mux.HandleFunc("POST /v1/webhooks", h.RequireAuth(h.CreateWebhook))
	mux.HandleFunc("GET /v1/webhooks", h.RequireAuth(h.ListWebhooks))
	mux.HandleFunc("DELETE /v1/webhooks/{id}", h.RequireAuth(h.DeleteWebhook))
	mux.HandleFunc("GET /v1/webhooks/{id}/deliveries", h.RequireAuth(h.ListWebhookDeliveries))

	// Google Calendar — connect/callback/status/disconnect
	mux.HandleFunc("GET /v1/calendar/connect", h.RequireAuth(h.ConnectCalendar))
	mux.HandleFunc("GET /v1/calendar/callback", h.CalendarCallback)
	mux.HandleFunc("GET /v1/calendar/status", h.RequireAuth(h.CalendarStatus))
	mux.HandleFunc("DELETE /v1/calendar", h.RequireAuth(h.DisconnectCalendar))

	// API keys
	mux.HandleFunc("GET /v1/api-keys", h.RequireAuth(h.ListAPIKeys))
	mux.HandleFunc("POST /v1/api-keys", h.RequireAuth(h.CreateAPIKey))
	mux.HandleFunc("DELETE /v1/api-keys/{id}", h.RequireAuth(h.DeleteAPIKey))

	// Admin SPA — served at /admin/* with SPA fallback for client-side routing.
	adminSPA := frontend.Handler()
	mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	mux.Handle("/admin/", http.StripPrefix("/admin", adminSPA))

	return RequestID(Logging(logger, mux)), drain
}

// seedSMTPToDB writes env-var SMTP settings into the DB on first boot so they
// appear in the UI. Uses WHERE smtp_host = '' to avoid a check-then-act race
// and to never overwrite settings the user has already saved via the UI.
func seedSMTPToDB(db *sql.DB, cfg *config.Config, encKey [32]byte, logger *slog.Logger) {
	var passEnc string
	if cfg.SMTPPass != "" {
		enc, err := secret.Encrypt(encKey, cfg.SMTPPass)
		if err != nil {
			logger.Warn("mailer: seed — could not encrypt password", "error", err)
			return
		}
		passEnc = enc
	}

	boolToInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	res, err := db.Exec(`
		UPDATE server_settings SET
		  smtp_host = ?, smtp_port = ?, smtp_user = ?, smtp_pass_enc = ?,
		  smtp_tls = ?, smtp_starttls = ?,
		  email_from = ?, email_from_name = ?,
		  updated_at = datetime('now')
		WHERE id = 1 AND smtp_host = ''`,
		cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, passEnc,
		boolToInt(cfg.SMTPTLS), boolToInt(cfg.SMTPStartTLS),
		cfg.EmailFrom, cfg.EmailFromName)
	if err != nil {
		logger.Warn("mailer: seed to database failed", "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		logger.Info("mailer: seeded SMTP settings from env vars into database (env vars can now be removed)")
	}
}
