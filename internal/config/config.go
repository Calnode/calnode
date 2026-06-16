package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port           string
	DatabaseURL    string
	EncryptionKey  string // platform secret (KEK input); vault derives the real AES key
	RecoverySecret string // CALNODE_RECOVERY_SECRET — escrow key stored in keystore
	BaseURL        string // identity host: OAuth callbacks, admin UI, team invites
	PublicBaseURL  string // booker-facing host: booking links, emails; defaults to BaseURL
	LogLevel      slog.Level

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

	// CookieSecure sets the Secure flag on session cookies. Defaults to true
	// when BASE_URL starts with https://, but can be overridden explicitly via
	// COOKIE_SECURE=false for HTTPS-terminated-at-proxy setups where the binary
	// itself listens on plain HTTP and BASE_URL is set correctly.
	CookieSecure bool
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
	}

	cfg.EncryptionKey = os.Getenv("CALNODE_ENCRYPTION_KEY")
	cfg.RecoverySecret = os.Getenv("CALNODE_RECOVERY_SECRET")
	// PUBLIC_BASE_URL overrides the booker-facing host (custom/vanity domain).
	// Unset → inherits BASE_URL, so single-domain deploys need only set BASE_URL.
	cfg.PublicBaseURL = getEnv("PUBLIC_BASE_URL", cfg.BaseURL)
	cfg.LogLevel = parseLogLevel(getEnv("LOG_LEVEL", "info"))
	cfg.CookieSecure = getBool("COOKIE_SECURE", strings.HasPrefix(cfg.BaseURL, "https://"))

	return cfg
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
