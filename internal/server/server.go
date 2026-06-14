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

	// Mailer is initialised first so the worker can use it for reminders.
	var mailSvc mailer.Mailer = &mailer.Noop{}
	if cfg.SMTPHost != "" {
		mailSvc = mailer.NewSMTP(
			cfg.SMTPHost, cfg.SMTPPort,
			cfg.SMTPUser, cfg.SMTPPass,
			cfg.SMTPTLS, cfg.SMTPStartTLS,
			cfg.EmailFrom, cfg.EmailFromName,
		)
		logger.Info("mailer configured", "host", cfg.SMTPHost, "port", cfg.SMTPPort)
	} else {
		logger.Info("mailer not configured — emails disabled (set EMAIL_SMTP_HOST to enable)")
	}
	h.SetMailer(mailSvc, cfg.BaseURL)

	drain := func() {}
	whs, err := webhook.New(db, cfg.EncryptionKey)
	if err != nil {
		logger.Error("webhook: init failed", "error", err)
	} else {
		h.SetWebhookSvc(whs)
		wrk := worker.New(db, whs, logger, worker.WithMailer(mailSvc))
		go wrk.Run(ctx)
		drain = wrk.Wait
		logger.Info("webhook worker started")
	}

	if cfg.GoogleClientID != "" {
		// Google sign-in (user sessions for admin UI).
		authRedirect := cfg.BaseURL + "/v1/auth/callback"
		h.SetGoogleAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, authRedirect, cfg.CookieSecure)
		logger.Info("Google OAuth login configured", "redirect_url", authRedirect)

		// Google Calendar (free/busy + event write).
		calRedirect := cfg.BaseURL + "/v1/calendar/callback"
		gc, err := gcal.New(db, cfg.GoogleClientID, cfg.GoogleClientSecret, calRedirect, cfg.EncryptionKey)
		if err != nil {
			logger.Error("gcal: init failed", "error", err)
		} else {
			h.SetCalendar(gc)
			logger.Info("Google Calendar configured", "redirect_url", calRedirect)
		}
	} else {
		logger.Info("Google OAuth not configured (set GOOGLE_CLIENT_ID to enable sign-in and calendar)")
	}

	// Ops (§16)
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)

	// Bootstrap — public, once-only
	mux.HandleFunc("POST /v1/setup", h.Setup)

	// Google OAuth login (browser sessions for admin UI).
	// Rate-limited to prevent state-cookie flooding and token-exchange quota abuse.
	authRL := RateLimit(10, time.Minute)
	mux.HandleFunc("GET /v1/auth/login", authRL(h.LoginGoogle))
	mux.HandleFunc("GET /v1/auth/callback", authRL(h.CallbackGoogle))
	mux.HandleFunc("POST /v1/auth/logout", h.Logout)

	// Users
	mux.HandleFunc("GET /v1/users/me", h.RequireAuth(h.GetMe))

	// Event types
	mux.HandleFunc("POST /v1/event-types", h.RequireAuth(h.CreateEventType))
	mux.HandleFunc("GET /v1/event-types", h.RequireAuth(h.ListEventTypes))
	mux.HandleFunc("GET /v1/event-types/{slug}", h.RequireAuth(h.GetEventType))
	mux.HandleFunc("PATCH /v1/event-types/{slug}", h.RequireAuth(h.PatchEventType))
	mux.HandleFunc("DELETE /v1/event-types/{slug}", h.RequireAuth(h.DeleteEventType))

	// Availability rules
	mux.HandleFunc("POST /v1/availability-rules", h.RequireAuth(h.CreateAvailabilityRule))
	mux.HandleFunc("GET /v1/availability-rules", h.RequireAuth(h.ListAvailabilityRules))
	mux.HandleFunc("PATCH /v1/availability-rules/{id}", h.RequireAuth(h.UpdateAvailabilityRule))
	mux.HandleFunc("DELETE /v1/availability-rules/{id}", h.RequireAuth(h.DeleteAvailabilityRule))

	// Availability overrides (date-specific blocks or custom hours)
	mux.HandleFunc("POST /v1/availability-overrides", h.RequireAuth(h.CreateAvailabilityOverride))
	mux.HandleFunc("GET /v1/availability-overrides", h.RequireAuth(h.ListAvailabilityOverrides))
	mux.HandleFunc("PATCH /v1/availability-overrides/{id}", h.RequireAuth(h.UpdateAvailabilityOverride))
	mux.HandleFunc("DELETE /v1/availability-overrides/{id}", h.RequireAuth(h.DeleteAvailabilityOverride))

	// Slots — public (no auth; event type must be is_public)
	mux.HandleFunc("GET /v1/event-types/{slug}/slots", h.GetSlots)

	// Intake questions — public list (booking form); admin list + CRUD require auth
	mux.HandleFunc("GET /v1/event-types/{slug}/questions", h.ListQuestions)
	mux.HandleFunc("GET /v1/event-types/{slug}/questions/admin", h.RequireAuth(h.ListQuestionsAdmin))
	mux.HandleFunc("POST /v1/event-types/{slug}/questions", h.RequireAuth(h.CreateQuestion))
	mux.HandleFunc("PATCH /v1/event-types/{slug}/questions/{id}", h.RequireAuth(h.UpdateQuestion))
	mux.HandleFunc("DELETE /v1/event-types/{slug}/questions/{id}", h.RequireAuth(h.DeleteQuestion))

	bookingRL := RateLimit(20, time.Minute)
	manageRL := RateLimit(30, time.Minute)

	// Bookings — create and get are public; list and cancel require auth
	mux.HandleFunc("POST /v1/bookings", bookingRL(h.CreateBooking))
	mux.HandleFunc("GET /v1/bookings/{id}", h.GetBooking)
	mux.HandleFunc("GET /v1/bookings", h.RequireAuth(h.ListBookings))
	mux.HandleFunc("POST /v1/bookings/{id}/cancel", h.RequireAuth(h.CancelBooking))
	mux.HandleFunc("PATCH /v1/bookings/{id}/reschedule", h.RequireAuth(h.RescheduleBooking))
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
