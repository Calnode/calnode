package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone database so scratch/distroless images work

	"github.com/joho/godotenv"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/server"
)

func main() {
	// Load .env if present (dev convenience). Real env vars always win.
	_ = godotenv.Load()

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
