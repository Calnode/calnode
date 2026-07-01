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
	"github.com/calnode/calnode/internal/livekit"
	"github.com/calnode/calnode/internal/llm"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/stripe"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/zoom"
)

type Handler struct {
	db            *sql.DB
	logger        *slog.Logger
	bookingSvc    *booking.Service
	mailer        mailer.Mailer
	live          *mailer.Live // non-nil in production; nil in tests using a direct stub
	encKey        [32]byte     // AES-256 key for encrypting secrets stored in the DB
	calMu         sync.RWMutex
	cal           *calendar.Service
	calNudge      chan struct{} // buffered(1): wakes the calendar reconciler after a failed inline op
	webhookSvc    *webhook.Service
	baseURL       string
	publicBaseURL string
	dataDir       string
	authMu        sync.RWMutex
	googleAuth    *oauth2.Config
	microsoftAuth *oauth2.Config
	secureCookie  bool
	llmMu         sync.RWMutex
	llm           *llm.Client // nil when the optional LLM layer is off/unconfigured
	zoomMu        sync.RWMutex
	zoom          *zoom.Client // nil when no Zoom app is configured
	stripeMu      sync.RWMutex
	stripe        *stripe.Client // nil when payments are unconfigured
	livekitMu     sync.RWMutex
	livekit       *livekit.Client // nil when LiveKit video is unconfigured
}

// SetLiveKit swaps the active LiveKit client (nil disables built-in video rooms).
// Hot-reloadable from the LiveKit settings page.
func (h *Handler) SetLiveKit(c *livekit.Client) {
	h.livekitMu.Lock()
	h.livekit = c
	h.livekitMu.Unlock()
	// Self-heal: any recording still 'active' is an orphan from before this restart (its egress
	// is no longer tracked), and would otherwise block the idempotent guard on its room forever.
	if c != nil {
		if _, err := h.db.Exec(
			`UPDATE recordings SET status = 'complete', updated_at = datetime('now') WHERE status = 'active'`); err != nil {
			h.logger.Warn("livekit: sweep stale recordings", "error", err)
		}
	}
}

// getLiveKit returns the active LiveKit client, or nil when video is unconfigured.
func (h *Handler) getLiveKit() *livekit.Client {
	h.livekitMu.RLock()
	defer h.livekitMu.RUnlock()
	return h.livekit
}

// SetStripe swaps the active Stripe client (nil disables paid bookings). Hot-reloadable
// from the Payments settings page.
func (h *Handler) SetStripe(c *stripe.Client) {
	h.stripeMu.Lock()
	h.stripe = c
	h.stripeMu.Unlock()
}

// getStripe returns the active Stripe client, or nil when payments are unconfigured.
func (h *Handler) getStripe() *stripe.Client {
	h.stripeMu.RLock()
	defer h.stripeMu.RUnlock()
	return h.stripe
}

// SetZoom swaps the active Zoom client (nil disables Zoom auto-minting). Hot-reloadable
// from the Zoom settings page.
func (h *Handler) SetZoom(c *zoom.Client) {
	h.zoomMu.Lock()
	h.zoom = c
	h.zoomMu.Unlock()
}

// getZoom returns the active Zoom client, or nil when no Zoom app is configured.
func (h *Handler) getZoom() *zoom.Client {
	h.zoomMu.RLock()
	defer h.zoomMu.RUnlock()
	return h.zoom
}

// SetLLM swaps the active LLM client (nil disables AI features). Hot-reloadable from
// the settings page.
func (h *Handler) SetLLM(c *llm.Client) {
	h.llmMu.Lock()
	h.llm = c
	h.llmMu.Unlock()
}

// getLLM returns the active LLM client, or nil when AI is off — callers MUST nil-check
// and fall back to the deterministic path.
func (h *Handler) getLLM() *llm.Client {
	h.llmMu.RLock()
	defer h.llmMu.RUnlock()
	return h.llm
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

// getMicrosoftAuth returns the current Microsoft OAuth config under a read lock.
func (h *Handler) getMicrosoftAuth() *oauth2.Config {
	h.authMu.RLock()
	defer h.authMu.RUnlock()
	return h.microsoftAuth
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

// requireAdmin resolves the authenticated caller and writes a 403 (returning
// ok=false, which the caller must check and return on) unless they're an admin.
// Every settings handler (branding/tracking/google/email/llm/storage/stripe/zoom/
// livekit/notetaker) must call this first, not repeat the check inline — copy-paste
// was exactly how GetEmailSettings shipped without it (a real, live gap, not a
// hypothetical one) while its sibling PATCH/test-connection handlers had it.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (AuthUser, bool) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return AuthUser{}, false
	}
	return user, true
}
