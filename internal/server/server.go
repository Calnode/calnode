package server

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/mailer"
)

func New(cfg *config.Config, db *sql.DB, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	h := handler.New(db, logger)

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

	// Ops (§16)
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)

	// Bootstrap — public, once-only
	mux.HandleFunc("POST /v1/setup", h.Setup)

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

	// Bookings — create and get are public; list and cancel require auth
	mux.HandleFunc("POST /v1/bookings", h.CreateBooking)
	mux.HandleFunc("GET /v1/bookings/{id}", h.GetBooking)
	mux.HandleFunc("GET /v1/bookings", h.RequireAuth(h.ListBookings))
	mux.HandleFunc("POST /v1/bookings/{id}/cancel", h.RequireAuth(h.CancelBooking))

	return RequestID(Logging(logger, mux))
}
