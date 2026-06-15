package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone database so scratch/distroless images work

	"github.com/joho/godotenv"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/server"
)

func main() {
	// Subcommand dispatch: `calnode reset-admin <email> <password>`
	if len(os.Args) > 1 && os.Args[1] == "reset-admin" {
		_ = godotenv.Load()
		runResetAdmin(os.Args[2:])
		return
	}

	// Load .env if present (dev convenience). Real env vars always win.
	_ = godotenv.Load()

	// Ensure a persistent encryption key exists before loading config.
	// If CALNODE_ENCRYPTION_KEY is not set, generate one and save it to .env
	// so it survives restarts. This is a one-time operation on first boot.
	ensureEncryptionKey()

	cfg := config.Load()

	if cfg.GoogleClientID != "" {
		slog.Info("Google OAuth configured", "client_id_prefix", cfg.GoogleClientID[:20])
	} else {
		slog.Warn("Google OAuth NOT configured — GOOGLE_CLIENT_ID is empty")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	logger.Info("database migrations applied")

	workerCtx, workerCancel := context.WithCancel(context.Background())

	srv, drainWorker := server.New(workerCtx, cfg, database, logger)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      srv,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server listening", "port", cfg.Port, "base_url", cfg.BaseURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			quit <- syscall.SIGTERM // triggers graceful shutdown so deferred db.Close() runs
		}
	}()

	<-quit
	logger.Info("shutting down — draining worker and in-flight requests")

	// Stop the worker's ticker loop and drain the HTTP server concurrently so a
	// slow poll cycle (up to 10 jobs × 10 s each) does not delay in-flight HTTP
	// request draining. Both must complete before the process exits.
	workerCancel()

	workerDone := make(chan struct{})
	go func() {
		drainWorker()
		close(workerDone)
	}()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("forced shutdown", "error", err)
		os.Exit(1)
	}

	<-workerDone // wait for current poll cycle to complete before db.Close()
	logger.Info("server stopped")
}

// ensureEncryptionKey generates CALNODE_ENCRYPTION_KEY on first boot and
// persists it to .env so it survives restarts. If the file write fails,
// the key is still set for this session and a clear warning is logged.
func ensureEncryptionKey() {
	if os.Getenv("CALNODE_ENCRYPTION_KEY") != "" {
		return // already set via real env var or .env
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("could not generate encryption key", "error", err)
		os.Exit(1)
	}
	key := hex.EncodeToString(b)

	if err := persistKeyToEnvFile(key); err != nil {
		slog.Warn("generated CALNODE_ENCRYPTION_KEY for this session — could not save to .env; add it manually to make it permanent",
			"error", err,
			"CALNODE_ENCRYPTION_KEY", key)
	} else {
		slog.Info("generated CALNODE_ENCRYPTION_KEY and saved to .env — back it up; losing it makes stored secrets unrecoverable")
	}

	os.Setenv("CALNODE_ENCRYPTION_KEY", key)
}

// persistKeyToEnvFile writes the key to .env, creating the file if it does
// not exist. If the key already appears with an empty value (e.g. copied from
// .env.example), that line is replaced in-place so comments are preserved.
func persistKeyToEnvFile(key string) error {
	const envFile = ".env"
	line := "CALNODE_ENCRYPTION_KEY=" + key

	content, err := os.ReadFile(envFile)
	if os.IsNotExist(err) {
		return os.WriteFile(envFile, []byte(line+"\n"), 0o600)
	}
	if err != nil {
		return err
	}

	// Replace an empty / commented-out entry so we don't create duplicates.
	// Matches: `CALNODE_ENCRYPTION_KEY=` or `# CALNODE_ENCRYPTION_KEY=` (trailing whitespace ok).
	re := regexp.MustCompile(`(?m)^#?\s*CALNODE_ENCRYPTION_KEY\s*=\s*$`)
	if re.Match(content) {
		return os.WriteFile(envFile, re.ReplaceAll(content, []byte(line)), 0o600)
	}

	// Key not in file at all — append.
	f, err := os.OpenFile(envFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n%s\n", line)
	return err
}
