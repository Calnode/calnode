package handler

import (
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync"

	"golang.org/x/oauth2"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
)

type Handler struct {
	db           *sql.DB
	logger       *slog.Logger
	bookingSvc   *booking.Service
	mailer       mailer.Mailer
	live         *mailer.Live // non-nil in production; nil in tests using a direct stub
	encKey       [32]byte     // AES-256 key for encrypting secrets stored in the DB
	calMu        sync.RWMutex
	cal          *calendar.Service
	calNudge     chan struct{} // buffered(1): wakes the calendar reconciler after a failed inline op
	webhookSvc   *webhook.Service
	baseURL       string
	publicBaseURL string
	dataDir       string
	authMu       sync.RWMutex
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
		calNudge:   make(chan struct{}, 1),
	}
}

// SetMailer configures the email sender and the base URL used in email links.
// If m is a *mailer.Live, it is also stored as h.live for hot-swap support.
func (h *Handler) SetMailer(m mailer.Mailer, baseURL string) {
	h.mailer = m
	h.baseURL = baseURL
	if l, ok := m.(*mailer.Live); ok {
		h.live = l
	}
}

// SetEncKey stores the AES-256 encryption key used for secrets in the DB.
func (h *Handler) SetEncKey(hexKey string) {
	if b, err := hex.DecodeString(hexKey); err == nil && len(b) == 32 {
		copy(h.encKey[:], b)
	}
	// If empty or invalid, encKey stays zero — suitable for dev/test.
}

// SetBaseURL sets the identity host used for OAuth redirects, admin UI links,
// and team invites.
func (h *Handler) SetBaseURL(url string) {
	h.baseURL = url
}

// SetPublicBaseURL sets the booker-facing host used for booking-page links and
// outbound email links. When empty, publicURL falls back to baseURL.
func (h *Handler) SetPublicBaseURL(url string) {
	h.publicBaseURL = url
}

// publicURL returns the booker-facing base URL, defaulting to the identity host
// (baseURL) when no public host has been configured.
func (h *Handler) publicURL() string {
	if h.publicBaseURL != "" {
		return h.publicBaseURL
	}
	return h.baseURL
}

// SetDataDir sets the directory used for file uploads (avatars, etc.).
func (h *Handler) SetDataDir(dir string) {
	h.dataDir = dir
}

// SetCalendar configures the multi-provider calendar service.
func (h *Handler) SetCalendar(c *calendar.Service) {
	h.calMu.Lock()
	h.cal = c
	h.calMu.Unlock()
}

// getCal returns the current calendar service under a read lock (nil if unconfigured).
func (h *Handler) getCal() *calendar.Service {
	h.calMu.RLock()
	defer h.calMu.RUnlock()
	return h.cal
}

// getGoogleAuth returns the current Google OAuth config under a read lock.
func (h *Handler) getGoogleAuth() *oauth2.Config {
	h.authMu.RLock()
	defer h.authMu.RUnlock()
	return h.googleAuth
}

// SetWebhookSvc replaces the default ephemeral-key webhook service with one
// backed by the configured encryption key.
func (h *Handler) SetWebhookSvc(svc *webhook.Service) {
	h.webhookSvc = svc
}

// isEmailEnabled reports whether a real SMTP sender is configured.
func (h *Handler) isEmailEnabled() bool {
	if h.live != nil {
		return h.live.IsEnabled()
	}
	// Fallback for tests that inject a direct stub mailer (not wrapped in Live).
	_, isNoop := h.mailer.(*mailer.Noop)
	return !isNoop
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}
