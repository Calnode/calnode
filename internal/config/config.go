package config

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"
)

type Config struct {
	Port          string
	DatabaseURL   string
	EncryptionKey string // 32-byte hex-encoded AES-GCM key
	BaseURL       string
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

	cfg.EncryptionKey = loadEncryptionKey()
	cfg.LogLevel = parseLogLevel(getEnv("LOG_LEVEL", "info"))

	return cfg
}

func loadEncryptionKey() string {
	key := os.Getenv("CALNODE_ENCRYPTION_KEY")
	if key != "" {
		return key
	}
	// Dev-only fallback: ephemeral random key. Tokens encrypted with this key
	// are lost on restart. Set CALNODE_ENCRYPTION_KEY for any persistent deployment.
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	ephemeral := hex.EncodeToString(b)
	slog.Warn("CALNODE_ENCRYPTION_KEY not set — using ephemeral key; calendar tokens will not survive restart; DO NOT use in production")
	return ephemeral
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
