package config_test

import (
	"log/slog"
	"os"
	"testing"
	"time"

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

func TestLoad_publicBaseURLDefaultsToBaseURL(t *testing.T) {
	t.Setenv("BASE_URL", "https://book.acme.com")
	os.Unsetenv("PUBLIC_BASE_URL")
	cfg := config.Load()
	if cfg.PublicBaseURL != "https://book.acme.com" {
		t.Errorf("PublicBaseURL = %q; want it to inherit BASE_URL", cfg.PublicBaseURL)
	}
}

func TestLoad_publicBaseURLOverride(t *testing.T) {
	t.Setenv("BASE_URL", "https://acme.app.calnode.com")
	t.Setenv("PUBLIC_BASE_URL", "https://book.acme.com")
	cfg := config.Load()
	if cfg.BaseURL != "https://acme.app.calnode.com" {
		t.Errorf("BaseURL = %q; want canonical host", cfg.BaseURL)
	}
	if cfg.PublicBaseURL != "https://book.acme.com" {
		t.Errorf("PublicBaseURL = %q; want the override", cfg.PublicBaseURL)
	}
}

func TestLoad_encryptionKeyAbsent(t *testing.T) {
	os.Unsetenv("CALNODE_ENCRYPTION_KEY")
	cfg := config.Load()
	// Config no longer auto-generates a key; the vault handles that at startup.
	// An empty EncryptionKey is valid here — dev mode gets an ephemeral key,
	// production fails fast inside keyvault.Open.
	if cfg.EncryptionKey != "" {
		t.Errorf("EncryptionKey should be empty when CALNODE_ENCRYPTION_KEY is unset; got %q", cfg.EncryptionKey)
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

func TestLoad_demoModeDefaults(t *testing.T) {
	os.Unsetenv("DEMO_MODE")
	os.Unsetenv("DEMO_RESET_INTERVAL")
	cfg := config.Load()
	if cfg.DemoMode {
		t.Error("DemoMode should default to false")
	}
	if cfg.DemoResetInterval != 30*time.Minute {
		t.Errorf("DemoResetInterval = %v; want 30m default", cfg.DemoResetInterval)
	}
}

func TestLoad_demoModeEnvOverrides(t *testing.T) {
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("DEMO_RESET_INTERVAL", "45s")
	cfg := config.Load()
	if !cfg.DemoMode {
		t.Error("DemoMode = false; want true")
	}
	if cfg.DemoResetInterval != 45*time.Second {
		t.Errorf("DemoResetInterval = %v; want 45s", cfg.DemoResetInterval)
	}
}

func TestLoad_demoResetIntervalInvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("DEMO_RESET_INTERVAL", "not-a-duration")
	cfg := config.Load()
	if cfg.DemoResetInterval != 30*time.Minute {
		t.Errorf("DemoResetInterval = %v; want 30m default on invalid input", cfg.DemoResetInterval)
	}
}
