package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone database so scratch/distroless images work

	"github.com/joho/godotenv"

	"github.com/calnode/calnode/internal/buildinfo"
	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/keyvault"
	"github.com/calnode/calnode/internal/server"
)

func main() {
	// Subcommand dispatch.
	if len(os.Args) > 1 {
		_ = godotenv.Load()
		switch os.Args[1] {
		case "reset-admin":
			runResetAdmin(os.Args[2:])
			return
		case "rotate-key":
			runRotateKey(os.Args[2:])
			return
		case "recover-key":
			runRecoverKey(os.Args[2:])
			return
		}
	}

	// Load .env if present (dev convenience). Real env vars always win.
	_ = godotenv.Load()

	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	bi := buildinfo.Get()
	logger.Info("starting calnode", "version", bi.Version, "commit", bi.Commit, "build_time", bi.BuildTime, "dirty", bi.Dirty)

	if cfg.GoogleClientID != "" {
		slog.Info("Google OAuth configured", "client_id_prefix", cfg.GoogleClientID[:20])
	} else {
		slog.Warn("Google OAuth NOT configured — GOOGLE_CLIENT_ID is empty")
	}

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

	// Open the key vault. devMode allows an ephemeral DEK when no secret is set
	// (handy for local development); production deployments must set
	// CALNODE_ENCRYPTION_KEY or the vault will refuse to start.
	devMode := !strings.HasPrefix(cfg.BaseURL, "https://")
	vault, err := keyvault.Open(database, cfg.EncryptionKey, cfg.RecoverySecret, devMode)
	if err != nil {
		logger.Error("keyvault: failed to open", "error", err)
		os.Exit(1)
	}
	// Replace the platform secret in cfg with the real DEK so all downstream
	// components (handler, gcal, webhook) receive the AES key they expect.
	cfg.EncryptionKey = vault.DEKHex()

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
