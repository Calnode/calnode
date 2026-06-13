package config_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/calnode/calnode/internal/config"
)

func TestLoad_defaults(t *testing.T) {
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("BASE_URL")
	os.Unsetenv("LOG_LEVEL")

	cfg := config.Load()

	if cfg.Port != "3000" {
		t.Errorf("Port = %q; want 3000", cfg.Port)
	}
	if cfg.DatabaseURL != "sqlite://./data/calnode.db" {
		t.Errorf("DatabaseURL = %q; want sqlite://./data/calnode.db", cfg.DatabaseURL)
	}
	if cfg.BaseURL != "http://localhost:3000" {
		t.Errorf("BaseURL = %q; want http://localhost:3000", cfg.BaseURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v; want INFO", cfg.LogLevel)
	}
}

func TestLoad_envOverrides(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("DATABASE_URL", "sqlite:///tmp/test.db")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := config.Load()

	if cfg.Port != "8080" {
		t.Errorf("Port = %q; want 8080", cfg.Port)
	}
	if cfg.DatabaseURL != "sqlite:///tmp/test.db" {
		t.Errorf("DatabaseURL = %q; want sqlite:///tmp/test.db", cfg.DatabaseURL)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v; want DEBUG", cfg.LogLevel)
	}
}

func TestLoad_encryptionKeyGenerated(t *testing.T) {
	os.Unsetenv("CALNODE_ENCRYPTION_KEY")
	cfg := config.Load()
	if cfg.EncryptionKey == "" {
		t.Error("EncryptionKey should be non-empty even when env var is unset")
	}
}

func TestLoad_encryptionKeyFromEnv(t *testing.T) {
	const validKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" // 64 hex chars = 32 bytes
	t.Setenv("CALNODE_ENCRYPTION_KEY", validKey)
	cfg := config.Load()
	if cfg.EncryptionKey != validKey {
		t.Errorf("EncryptionKey = %q; want %q", cfg.EncryptionKey, validKey)
	}
}
