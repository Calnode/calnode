package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
)

type Handler struct {
	db         *sql.DB
	logger     *slog.Logger
	bookingSvc *booking.Service
	mailer     mailer.Mailer
	baseURL    string
}

func New(db *sql.DB, logger *slog.Logger) *Handler {
	return &Handler{
		db:         db,
		logger:     logger,
		bookingSvc: booking.New(db),
		mailer:     &mailer.Noop{},
	}
}

// SetMailer configures the email sender and the base URL used in email links.
// Called from server.New when SMTP is configured; the default is a no-op sender.
func (h *Handler) SetMailer(m mailer.Mailer, baseURL string) {
	h.mailer = m
	h.baseURL = baseURL
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}
