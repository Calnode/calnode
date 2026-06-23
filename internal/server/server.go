package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/calnode/calnode/frontend"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/calendar/microsoft"
	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/llm"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"

	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
	"github.com/calnode/calnode/internal/zoom"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the HTTP mux and starts background services. It returns the
// handler and a drain function that blocks until the background worker has
// finished its current poll cycle — call drain before httpServer.Shutdown.
// BuildHandler constructs the fully-wired Handler — mailer, webhook worker, calendar
// providers, OAuth — without registering HTTP routes or starting an HTTP server. New
// uses it to back the HTTP server; the `calnode mcp` subcommand uses it to serve the
// MCP server over stdio. The returned drain func blocks until the background worker
// has finished its current poll cycle.
func BuildHandler(ctx context.Context, cfg *config.Config, db *sql.DB, logger *slog.Logger) (*handler.Handler, func()) {
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

	// Build one calendar Service and register every configured provider into it.
	calSvc := calendar.NewService(db)
	calRedirect := cfg.BaseURL + "/v1/calendar/callback"

	if googleClientID != "" {
		authRedirect := cfg.BaseURL + "/v1/auth/callback"
		h.SetGoogleAuth(googleClientID, googleClientSecret, authRedirect, cfg.CookieSecure)
		logger.Info("Google OAuth login configured", "redirect_url", authRedirect)

		gc, err := gcal.New(db, googleClientID, googleClientSecret, calRedirect, cfg.EncryptionKey)
		if err != nil {
			logger.Error("gcal: init failed", "error", err)
		} else {
			calSvc.Register(gc)
			logger.Info("Google Calendar configured", "redirect_url", calRedirect)
		}
	} else {
		logger.Info("Google OAuth not configured — add credentials in Settings or set GOOGLE_CLIENT_ID")
	}

	if cfg.MicrosoftClientID != "" && cfg.MicrosoftClientSecret != "" {
		msAuthRedirect := cfg.BaseURL + "/v1/auth/microsoft/callback"
		h.SetMicrosoftAuth(cfg.MicrosoftClientID, cfg.MicrosoftClientSecret, cfg.MicrosoftTenant, msAuthRedirect, cfg.CookieSecure)
		logger.Info("Microsoft OAuth login configured", "redirect_url", msAuthRedirect)

		mc, err := microsoft.New(db, cfg.MicrosoftClientID, cfg.MicrosoftClientSecret, cfg.MicrosoftTenant, calRedirect, cfg.EncryptionKey)
		if err != nil {
			logger.Error("microsoft: init failed", "error", err)
		} else {
			calSvc.Register(mc)
			logger.Info("Microsoft 365 calendar configured", "tenant", cfg.MicrosoftTenant)
		}
	}

	if calSvc.Any() {
		h.SetCalendar(calSvc)
		h.StartCalendarReconciler(ctx)
	}

	// Optional LLM layer (PRD §8.11) — off unless configured + enabled in Settings.
	if llmCfg, err := handler.LoadLLMSettingsFromDB(db, encKey); err != nil {
		logger.Warn("llm: could not load settings from database", "error", err)
	} else if llmCfg != nil && llmCfg.Enabled && llmCfg.Endpoint != "" {
		h.SetLLM(llm.New(llm.Config{Endpoint: llmCfg.Endpoint, Model: llmCfg.Model, APIKey: llmCfg.APIKey}))
		logger.Info("LLM layer enabled", "endpoint", llmCfg.Endpoint, "model", llmCfg.Model)
	} else {
		logger.Info("LLM layer not enabled — configure it in Settings → AI")
	}

	// Optional Zoom integration — each host connects their own Zoom account to
	// auto-mint meeting links. DB settings take priority over env vars.
	zoomClientID := cfg.ZoomClientID
	zoomClientSecret := cfg.ZoomClientSecret
	if dbZoom, dbZoomErr := handler.LoadZoomSettingsFromDB(db, encKey); dbZoomErr != nil {
		logger.Warn("zoom settings: could not load from database", "error", dbZoomErr)
	} else if dbZoom != nil {
		zoomClientID = dbZoom.ClientID
		zoomClientSecret = dbZoom.ClientSecret
		logger.Info("Zoom OAuth: credentials loaded from database")
	}
	if zoomClientID != "" && zoomClientSecret != "" {
		zoomRedirect := cfg.BaseURL + "/v1/zoom/callback"
		if zc, err := zoom.New(db, zoomClientID, zoomClientSecret, zoomRedirect, cfg.EncryptionKey); err != nil {
			logger.Error("zoom: init failed", "error", err)
		} else {
			h.SetZoom(zc)
			logger.Info("Zoom integration configured", "redirect_url", zoomRedirect)
		}
	} else {
		logger.Info("Zoom not configured — add credentials in Settings → Zoom")
	}

	return h, drain
}

// New wires services via BuildHandler, then registers all HTTP routes. It returns the
// http.Handler and the worker drain func.
func New(ctx context.Context, cfg *config.Config, db *sql.DB, logger *slog.Logger) (http.Handler, func()) {
	h, drain := BuildHandler(ctx, cfg, db, logger)
	mux := http.NewServeMux()

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

	// OAuth login (browser sessions for admin UI).
	authRL := RateLimit(10, time.Minute)
	mux.HandleFunc("GET /v1/auth/login", authRL(h.LoginGoogle))
	mux.HandleFunc("GET /v1/auth/callback", authRL(h.CallbackGoogle))
	mux.HandleFunc("GET /v1/auth/microsoft/login", authRL(h.LoginMicrosoft))
	mux.HandleFunc("GET /v1/auth/microsoft/callback", authRL(h.CallbackMicrosoft))
	mux.HandleFunc("POST /v1/auth/logout", h.Logout)

	// MCP server (Model Context Protocol) — Streamable HTTP transport for remote
	// agents. One server instance reused across requests. Guarded by a bearer token:
	// an OAuth access token (the "Connect" flow) or a cno_ API key, both resolved by
	// verifyMCPBearer. A 401 advertises the OAuth discovery doc so clients can connect.
	mcpSrv := h.MCPServer()
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	mcpAuth := auth.RequireBearerToken(h.VerifyMCPBearer, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.BaseURL + "/.well-known/oauth-protected-resource",
	})
	// RequireBearerToken authenticates; MCPCallerMiddleware then binds the user+role so
	// the tools scope by role (members → only their own bookings).
	mux.Handle("/mcp", mcpAuth(h.MCPCallerMiddleware(mcpHTTP)))

	// OAuth 2.1 authorization server for MCP (discovery + dynamic client registration;
	// the interactive /oauth/authorize + /oauth/token live with the consent flow). All
	// public — the security gate is the user login + consent at /oauth/authorize.
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.OAuthProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.OAuthAuthServerMetadata)
	oauthRegRL := RateLimit(20, time.Minute)
	mux.HandleFunc("POST /oauth/register", oauthRegRL(h.RegisterOAuthClient))
	mux.HandleFunc("GET /oauth/authorize", h.AuthorizeMCP)
	mux.HandleFunc("POST /oauth/authorize", h.AuthorizeMCPDecision)
	tokenRL := RateLimit(30, time.Minute)
	mux.HandleFunc("POST /oauth/token", tokenRL(h.TokenMCP))

	// Password management.
	mux.HandleFunc("POST /v1/users/me/password", h.RequireAuth(h.ChangePassword))
	mux.HandleFunc("POST /v1/users/{id}/password", h.RequireAuth(h.AdminSetPassword))

	// Invite management.
	inviteRL := RateLimit(20, time.Minute)
	mux.HandleFunc("POST /v1/invites", inviteRL(h.RequireAuth(h.CreateInvite)))
	mux.HandleFunc("GET /v1/invites", h.RequireAuth(h.ListInvites))
	mux.HandleFunc("DELETE /v1/invites/{id}", h.RequireAuth(h.RevokeInvite))
	mux.HandleFunc("POST /v1/invites/{id}/resend", inviteRL(h.RequireAuth(h.ResendInvite)))
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
	mux.HandleFunc("GET /branding/logo", h.ServeBrandingLogo)

	// Server settings — email (SMTP) and Google OAuth
	settingsRL := RateLimit(20, time.Minute)
	mux.HandleFunc("GET /v1/settings/email", h.RequireAuth(h.GetEmailSettings))
	mux.HandleFunc("PATCH /v1/settings/email", settingsRL(h.RequireAuth(h.PatchEmailSettings)))
	mux.HandleFunc("POST /v1/settings/email/test", settingsRL(h.RequireAuth(h.TestEmailConnection)))
	mux.HandleFunc("GET /v1/settings/google", h.RequireAuth(h.GetGoogleSettings))
	mux.HandleFunc("PATCH /v1/settings/google", settingsRL(h.RequireAuth(h.PatchGoogleSettings)))
	mux.HandleFunc("GET /v1/settings/zoom", h.RequireAuth(h.GetZoomSettings))
	mux.HandleFunc("PATCH /v1/settings/zoom", settingsRL(h.RequireAuth(h.PatchZoomSettings)))
	mux.HandleFunc("GET /v1/settings/tracking", h.RequireAuth(h.GetTrackingSettings))
	mux.HandleFunc("PATCH /v1/settings/tracking", settingsRL(h.RequireAuth(h.PatchTrackingSettings)))
	mux.HandleFunc("GET /v1/settings/llm", h.RequireAuth(h.GetLLMSettings))
	mux.HandleFunc("PATCH /v1/settings/llm", settingsRL(h.RequireAuth(h.PatchLLMSettings)))
	mux.HandleFunc("POST /v1/settings/llm/test", settingsRL(h.RequireAuth(h.TestLLMSettings)))
	mux.HandleFunc("GET /v1/settings/branding", h.RequireAuth(h.GetBranding))
	mux.HandleFunc("PATCH /v1/settings/branding", settingsRL(h.RequireAuth(h.PatchBranding)))
	mux.HandleFunc("POST /v1/settings/branding/logo", settingsRL(h.RequireAuth(h.UploadBrandingLogo)))
	mux.HandleFunc("DELETE /v1/settings/branding/logo", h.RequireAuth(h.DeleteBrandingLogo))

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

	// Slots — public, rate-limited per IP. Browsed more than booked (so a higher cap
	// than booking), but each call fans out Google free/busy per host, so leaving it
	// cors wraps the public booking endpoints so the embeddable widget can call them
	// cross-origin from a customer's site. Scoped to these unauthenticated routes
	// only — admin/auth routes never get CORS. Default (empty allowlist) = any origin.
	cors := PublicCORS(cfg.EmbedAllowedOrigins)

	// Public event-type display info for the widget (name/duration/location/brand).
	mux.HandleFunc("GET /v1/event-types/{slug}/public", cors(h.PublicEventType))

	// unthrottled is a CPU + API-quota abuse vector on an openly-public page.
	slotsRL := RateLimit(60, time.Minute)
	mux.HandleFunc("GET /v1/event-types/{slug}/slots", cors(slotsRL(h.GetSlots)))

	// Conversational booking assistant (optional AI; public, anonymous → tighter limit).
	assistantRL := RateLimit(15, time.Minute)
	mux.HandleFunc("POST /v1/event-types/{slug}/assistant", cors(assistantRL(h.BookingAssistant)))
	mux.HandleFunc("OPTIONS /v1/event-types/{slug}/assistant", cors(func(http.ResponseWriter, *http.Request) {}))

	// Intake questions
	mux.HandleFunc("GET /v1/event-types/{slug}/questions", cors(h.ListQuestions))
	mux.HandleFunc("GET /v1/event-types/{slug}/questions/admin", h.RequireAuth(h.ListQuestionsAdmin))
	mux.HandleFunc("POST /v1/event-types/{slug}/questions", h.RequireAuth(h.CreateQuestion))
	mux.HandleFunc("PATCH /v1/event-types/{slug}/questions/{id}", h.RequireAuth(h.UpdateQuestion))
	mux.HandleFunc("DELETE /v1/event-types/{slug}/questions/{id}", h.RequireAuth(h.DeleteQuestion))

	bookingRL := RateLimit(20, time.Minute)
	manageRL := RateLimit(30, time.Minute)

	// Bookings — public create is CORS-enabled for the widget; the JSON body makes it
	// a non-simple request, so the OPTIONS preflight is handled too.
	mux.HandleFunc("POST /v1/bookings", cors(bookingRL(h.CreateBooking)))
	mux.HandleFunc("OPTIONS /v1/bookings", cors(func(http.ResponseWriter, *http.Request) {}))
	mux.HandleFunc("GET /v1/bookings/{id}", h.GetBooking)
	mux.HandleFunc("GET /v1/bookings", h.RequireAuth(h.ListBookings))
	mux.HandleFunc("POST /v1/bookings/{id}/cancel", h.RequireAuth(h.CancelBooking))
	mux.HandleFunc("PATCH /v1/bookings/{id}/reschedule", h.RequireAuth(h.RescheduleBooking))
	mux.HandleFunc("POST /v1/bookings/{id}/reassign", h.RequireAuth(h.ReassignBooking))
	mux.HandleFunc("GET /v1/bookings/{id}/answers", h.RequireAuth(h.GetBookingAnswers))

	// Public booking page
	mux.HandleFunc("GET /embed.js", h.EmbedJS)
	mux.HandleFunc("GET /booking.css", h.BookingCSS)
	mux.HandleFunc("GET /book/{slug}", h.BookPage)

	// Manage booking (reschedule / cancel via token link)
	mux.HandleFunc("GET /manage/{token}", manageRL(h.ManagePage))
	mux.HandleFunc("POST /manage/{token}/reschedule", h.RescheduleByToken)
	mux.HandleFunc("POST /manage/{token}/cancel", h.CancelByToken)

	// Webhooks
	mux.HandleFunc("POST /v1/webhooks", h.RequireAuth(h.CreateWebhook))
	mux.HandleFunc("GET /v1/webhooks", h.RequireAuth(h.ListWebhooks))
	mux.HandleFunc("PATCH /v1/webhooks/{id}", h.RequireAuth(h.PatchWebhook))
	mux.HandleFunc("DELETE /v1/webhooks/{id}", h.RequireAuth(h.DeleteWebhook))
	mux.HandleFunc("GET /v1/webhooks/{id}/deliveries", h.RequireAuth(h.ListWebhookDeliveries))

	// Google Calendar — connect/callback/status/disconnect
	mux.HandleFunc("GET /v1/calendar/connect", h.RequireAuth(h.ConnectCalendar))
	mux.HandleFunc("GET /v1/calendar/callback", h.CalendarCallback)
	mux.HandleFunc("GET /v1/calendar/status", h.RequireAuth(h.CalendarStatus))
	mux.HandleFunc("DELETE /v1/calendar", h.RequireAuth(h.DisconnectCalendar))

	// Zoom — per-host OAuth connect (auto-mint meeting links).
	mux.HandleFunc("GET /v1/zoom/connect", h.RequireAuth(h.ConnectZoom))
	mux.HandleFunc("GET /v1/zoom/callback", h.ZoomCallback)
	mux.HandleFunc("GET /v1/zoom/status", h.RequireAuth(h.ZoomStatus))
	mux.HandleFunc("DELETE /v1/zoom", h.RequireAuth(h.DisconnectZoom))

	// API keys
	mux.HandleFunc("GET /v1/api-keys", h.RequireAuth(h.ListAPIKeys))
	mux.HandleFunc("POST /v1/api-keys", h.RequireAuth(h.CreateAPIKey))
	mux.HandleFunc("DELETE /v1/api-keys/{id}", h.RequireAuth(h.DeleteAPIKey))

	// Connected apps — MCP OAuth grants the user can review and revoke.
	mux.HandleFunc("GET /v1/oauth/connections", h.RequireAuth(h.ListOAuthConnections))
	mux.HandleFunc("DELETE /v1/oauth/connections/{id}", h.RequireAuth(h.RevokeOAuthConnection))

	// Favicon at the root, shared by the public server-rendered pages and the
	// browser's default /favicon.ico probe — same embedded source as the admin SPA.
	favicon := frontend.FaviconHandler()
	mux.Handle("GET /favicon.svg", favicon)
	mux.Handle("GET /favicon.ico", favicon)

	// Admin SPA — served at /admin/* with SPA fallback for client-side routing.
	adminSPA := frontend.Handler()
	mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	mux.Handle("/admin/", http.StripPrefix("/admin", adminSPA))

	// Bare root → admin. The `{$}` anchor matches ONLY the exact path "/", so it
	// stays a no-op for every other unmatched path (those still 404). Public
	// visitors always arrive via a full /book/{slug} link, so this only affects
	// an operator landing on the domain root. 302 (not 301) so it isn't cached
	// permanently if a marketing landing page is ever added here.
	mux.Handle("GET /{$}", http.RedirectHandler("/admin/", http.StatusFound))

	return RequestID(Logging(logger, SameOriginCheck(mux))), drain
}

// seedSMTPToDB writes env-var SMTP settings into the DB on first boot so they
// appear in the UI. Uses WHERE smtp_host = ” to avoid a check-then-act race
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
