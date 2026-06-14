package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

func New(cfg *config.Config, db *sql.DB, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	h := handler.New(db, logger)
	h.SetBaseURL(cfg.BaseURL)

	whs, err := webhook.New(db, cfg.EncryptionKey)
	if err != nil {
		logger.Error("webhook: init failed", "error", err)
	} else {
		h.SetWebhookSvc(whs)
		wrk := worker.New(db, whs, logger)
		go wrk.Run(context.Background())
		logger.Info("webhook worker started")
	}

	if cfg.SMTPHost != "" {
		m := mailer.NewSMTP(
			cfg.SMTPHost, cfg.SMTPPort,
			cfg.SMTPUser, cfg.SMTPPass,
			cfg.SMTPTLS, cfg.SMTPStartTLS,
			cfg.EmailFrom, cfg.EmailFromName,
		)
		h.SetMailer(m, cfg.BaseURL)
		logger.Info("mailer configured", "host", cfg.SMTPHost, "port", cfg.SMTPPort)
	} else {
		logger.Info("mailer not configured — emails disabled (set EMAIL_SMTP_HOST to enable)")
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
	mux.HandleFunc("DELETE /v1/availability-rules/{id}", h.RequireAuth(h.DeleteAvailabilityRule))

	// Slots — public (no auth; event type must be is_public)
	mux.HandleFunc("GET /v1/event-types/{slug}/slots", h.GetSlots)

	bookingRL := RateLimit(20, time.Minute)
	manageRL := RateLimit(30, time.Minute)

	// Bookings — create and get are public; list and cancel require auth
	mux.HandleFunc("POST /v1/bookings", bookingRL(h.CreateBooking))
	mux.HandleFunc("GET /v1/bookings/{id}", h.GetBooking)
	mux.HandleFunc("GET /v1/bookings", h.RequireAuth(h.ListBookings))
	mux.HandleFunc("POST /v1/bookings/{id}/cancel", h.RequireAuth(h.CancelBooking))

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

	return RequestID(Logging(logger, mux))
}
