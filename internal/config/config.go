package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port           string
	DatabaseURL    string
	EncryptionKey  string // platform secret (KEK input); vault derives the real AES key
	RecoverySecret string // CALNODE_RECOVERY_SECRET — escrow key stored in keystore
	BaseURL        string // identity host: OAuth callbacks, admin UI, team invites
	PublicBaseURL  string // booker-facing host: booking links, emails; defaults to BaseURL
	LogLevel       slog.Level

	// Email / SMTP
	SMTPHost      string
	SMTPPort      string
	SMTPUser      string
	SMTPPass      string
	SMTPTLS       bool // implicit TLS (port 465)
	SMTPStartTLS  bool // STARTTLS (port 587)
	EmailFrom     string
	EmailFromName string

	// Google OAuth (calendar + sign-in)
	GoogleClientID     string
	GoogleClientSecret string

	// Microsoft 365 / Outlook (calendar) — env-only; tenant defaults to "common".
	MicrosoftClientID     string
	MicrosoftClientSecret string
	MicrosoftTenant       string

	// Zoom OAuth app (per-host meeting links) — DB settings take priority over these.
	ZoomClientID     string
	ZoomClientSecret string

	// DataDir is where uploaded files (avatars, branding assets) are written.
	// Defaults to the relative directory "data", which is what every existing
	// deployment has always used; set DATA_DIR when the process runs somewhere
	// its working directory is not writable, such as a read-only container image
	// that mounts a volume elsewhere.
	DataDir string

	// CookieSecure sets the Secure flag on session cookies. Defaults to true
	// when BASE_URL starts with https://, but can be overridden explicitly via
	// COOKIE_SECURE=false for HTTPS-terminated-at-proxy setups where the binary
	// itself listens on plain HTTP and BASE_URL is set correctly.
	CookieSecure bool

	// EmbedAllowedOrigins lists the origins permitted to call the public booking
	// endpoints cross-origin (for the embeddable widget). Empty ⇒ allow any origin
	// (`*`). CORS only constrains browsers; it is not an access-control boundary —
	// the public endpoints are rate-limited regardless. Comma-separated.
	EmbedAllowedOrigins []string

	// TrustedProxyCIDRs lists the networks whose forwarded headers are believed when
	// resolving the client IP for per-IP rate limiting. Empty (the default) ⇒ the limit
	// keys on the TCP peer and X-Forwarded-For is ignored entirely, because a header
	// from an unvetted peer is a client-chosen value. Comma-separated CIDRs; a bare
	// address is taken as a single host. A fronting CDN's own ranges belong here: the
	// walk steps over its edge address and lands on the visitor.
	TrustedProxyCIDRs []string

	// FrameAncestors lists the origins allowed to embed the admin SPA in a frame, as a
	// Content-Security-Policy frame-ancestors source list. Space-separated, matching the
	// CSP syntax it becomes. Empty (the default) ⇒ nothing is sent and /admin/ behaves
	// exactly as it did. Each entry must be `https://host[:port]` or `'self'`; anything
	// else fails Validate and the process refuses to start, because a directive the
	// browser cannot parse is a directive that silently allows everything.
	//
	// Only the admin SPA is affected. The public booking pages keep their
	// `frame-ancestors 'none'` + `X-Frame-Options: DENY` unconditionally — those are
	// unauthenticated pages that take payment details, and no operator convenience is
	// worth making them embeddable.
	//
	// ⛔ Permitting the frame is not the same as making it work, and this setting is only
	// the first half. calnode_session is SameSite=Lax, so a browser withholds it from a
	// subresource request made by a CROSS-SITE parent: an operator who lists a console on
	// an unrelated registrable domain gets the frame they asked for and a login screen
	// inside it, with nothing in the response explaining why. Same-site parents ('self',
	// or a host under BASE_URL's registrable domain) are the working case. The fix would
	// be SameSite=None on the session cookie, which withdraws the CSRF protection Lax
	// gives every other route, so it is not on offer here.
	FrameAncestors []string

	// DemoMode turns this instance into a public, self-resetting demo: seeds sample
	// data on every boot (there's no persistent volume, so every boot is a fresh DB),
	// disables calendar/Zoom connect, serves a disallow-all robots.txt, and exposes
	// demo_mode/next_reset_at via the public auth-status endpoint so the frontend can
	// show the reset banner. Never set this on a real deployment.
	DemoMode bool
	// DemoResetInterval is how often DemoMode wipes and re-seeds the DB. Configurable
	// (not hardcoded to 30m) so local verification doesn't require waiting half an hour.
	DemoResetInterval time.Duration
}

func Load() *Config {
	cfg := &Config{
		Port:        getEnv("PORT", "3000"),
		DatabaseURL: getEnv("DATABASE_URL", "sqlite://./data/calnode.db"),
		BaseURL:     getEnv("BASE_URL", "http://localhost:3000"),

		SMTPHost:      getEnv("EMAIL_SMTP_HOST", ""),
		SMTPPort:      getEnv("EMAIL_SMTP_PORT", "587"),
		SMTPUser:      getEnv("EMAIL_SMTP_USER", ""),
		SMTPPass:      getEnv("EMAIL_SMTP_PASS", ""),
		SMTPTLS:       getBool("EMAIL_SMTP_TLS", false),
		SMTPStartTLS:  getBool("EMAIL_SMTP_STARTTLS", false),
		EmailFrom:     getEnv("EMAIL_FROM_ADDRESS", "bookings@localhost"),
		EmailFromName: getEnv("EMAIL_FROM_NAME", "Calnode"),

		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),

		MicrosoftClientID:     getEnv("MICROSOFT_CLIENT_ID", ""),
		MicrosoftClientSecret: getEnv("MICROSOFT_CLIENT_SECRET", ""),
		MicrosoftTenant:       getEnv("MICROSOFT_TENANT", "common"),

		ZoomClientID:     getEnv("ZOOM_CLIENT_ID", ""),
		ZoomClientSecret: getEnv("ZOOM_CLIENT_SECRET", ""),

		EmbedAllowedOrigins: splitCSV(getEnv("EMBED_ALLOWED_ORIGINS", "")),
		DataDir:             getEnv("DATA_DIR", "data"),
		TrustedProxyCIDRs:   splitCSV(getEnv("TRUSTED_PROXY_CIDRS", "")),
		// Space-separated, not comma: the value goes into a CSP source list verbatim, so
		// it reads the same in the env var as it does in the header.
		FrameAncestors: strings.Fields(getEnv("FRAME_ANCESTORS", "")),
	}

	cfg.EncryptionKey = os.Getenv("CALNODE_ENCRYPTION_KEY")
	cfg.RecoverySecret = os.Getenv("CALNODE_RECOVERY_SECRET")
	// PUBLIC_BASE_URL overrides the booker-facing host (custom/vanity domain).
	// Unset → inherits BASE_URL, so single-domain deploys need only set BASE_URL.
	cfg.PublicBaseURL = getEnv("PUBLIC_BASE_URL", cfg.BaseURL)
	cfg.LogLevel = parseLogLevel(getEnv("LOG_LEVEL", "info"))
	cfg.CookieSecure = getBool("COOKIE_SECURE", strings.HasPrefix(cfg.BaseURL, "https://"))
	cfg.DemoMode = getBool("DEMO_MODE", false)
	cfg.DemoResetInterval = getDuration("DEMO_RESET_INTERVAL", 30*time.Minute)

	return cfg
}

// Validate reports the configuration errors an operator has to fix before the process
// can safely serve traffic. Called from main after Load; a non-nil error is fatal.
//
// It holds the settings whose wrong value is worse than their absence. A malformed CSP
// directive is the example: browsers drop a source list they cannot parse, so the admin
// UI would end up MORE embeddable than with the setting unset, and nothing in the
// response would say so.
func (c *Config) Validate() error {
	for _, origin := range c.FrameAncestors {
		if err := validFrameAncestor(origin); err != nil {
			return fmt.Errorf("FRAME_ANCESTORS: %w", err)
		}
	}
	return nil
}

// validFrameAncestor accepts 'self' or an https origin with no path, credentials, query
// or fragment.
//
// Wildcards are deliberately refused even though CSP allows them. `https://*.example.com`
// trusts every host any subdomain of that name ever points at, including one taken over
// later; an operator who needs two hosts can name two hosts. Plain http is refused for
// the same reason the admin session cookie is Secure — the framing page would be able to
// read nothing, but its own compromise becomes a foothold.
func validFrameAncestor(origin string) error {
	if origin == "'self'" {
		return nil
	}
	if strings.HasPrefix(origin, "'") {
		// 'none', 'unsafe-inline' and friends are keywords this setting has no use for:
		// 'none' is not "unset" (see FrameAncestors) and the rest are not source
		// expressions at all. Refusing them keeps the accepted grammar one line long.
		return fmt.Errorf("%q is not a supported keyword; use 'self' or an https:// origin", origin)
	}
	u, err := url.Parse(origin)
	switch {
	case err != nil:
		return fmt.Errorf("%q is not a URL: %w", origin, err)
	case u.Scheme != "https":
		return fmt.Errorf("%q must use https://", origin)
	case u.Host == "":
		return fmt.Errorf("%q has no host", origin)
	case strings.Contains(u.Host, "*"):
		return fmt.Errorf("%q must name one host, not a wildcard", origin)
	case u.User != nil:
		return fmt.Errorf("%q must not carry credentials", origin)
	case u.Path != "" && u.Path != "/":
		return fmt.Errorf("%q must be an origin, with no path", origin)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("%q must be an origin, with no query or fragment", origin)
	}
	return nil
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// splitCSV parses a comma-separated value into trimmed, non-empty entries.
// Returns nil for an empty string.
func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
