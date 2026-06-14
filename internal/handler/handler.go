package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
)

type Handler struct {
	db         *sql.DB
	logger     *slog.Logger
	bookingSvc *booking.Service
	mailer     mailer.Mailer
	gcal       *gcal.Client
	webhookSvc *webhook.Service
	baseURL    string
	googleAuth   *oauth2.Config
	secureCookie bool
}

func New(db *sql.DB, logger *slog.Logger) *Handler {
	whs, _ := webhook.New(db, "") // ephemeral key when no encryption key configured
	return &Handler{
		db:         db,
		logger:     logger,
		bookingSvc: booking.New(db),
		mailer:     &mailer.Noop{},
		webhookSvc: whs,
	}
}

// SetMailer configures the email sender and the base URL used in email links.
// Called from server.New when SMTP is configured; the default is a no-op sender.
func (h *Handler) SetMailer(m mailer.Mailer, baseURL string) {
	h.mailer = m
	h.baseURL = baseURL
}

// SetBaseURL sets the base URL used in redirects and email links.
// Called from server.New before any SMTP or GCal init.
func (h *Handler) SetBaseURL(url string) {
	h.baseURL = url
}

// SetCalendar configures the Google Calendar client.
// Called from server.New when GOOGLE_CLIENT_ID is set.
func (h *Handler) SetCalendar(c *gcal.Client) {
	h.gcal = c
}

// SetWebhookSvc replaces the default ephemeral-key webhook service with one
// backed by the configured encryption key. Called from server.New.
func (h *Handler) SetWebhookSvc(svc *webhook.Service) {
	h.webhookSvc = svc
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}
