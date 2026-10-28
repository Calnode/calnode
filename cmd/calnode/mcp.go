package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/keyvault"
	"github.com/calnode/calnode/internal/server"
)

// runMCPStdio is invoked when the binary is called as:
//
//	calnode mcp
//
// It boots the same service stack as the HTTP server (database, key vault, calendar
// providers, mailer, webhook worker) and serves the Model Context Protocol over stdio
// for a local agent. The MCP JSON-RPC stream owns stdout, so all logs go to stderr.
func runMCPStdio(_ []string) {
	cfg := config.Load()

	// stdout carries the MCP protocol — never log to it. All logs → stderr.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Error("mcp: failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		logger.Error("mcp: failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Resolve the platform secret the same way the server does, so `calnode mcp`
	// and `calnode serve` always agree on which key unlocks the vault. Diverging
	// here would let one process read what the other wrote only by accident.
	platformSecret, secretSource, secretPath, err := keyvault.ResolvePlatformSecret(
		cfg.EncryptionKey, db.FilePath(cfg.DatabaseURL))
	if err != nil {
		logger.Error("mcp: cannot determine the platform secret", "error", err)
		os.Exit(1)
	}
	if secretSource == keyvault.SecretFromGenerated {
		logger.Warn("mcp: generated and persisted a platform secret", "path", secretPath)
	}

	// Open the key vault (same dev-mode rule as the server) and swap the real DEK into
	// cfg so calendar/webhook token crypto works.
	devMode := !strings.HasPrefix(cfg.BaseURL, "https://")
	vault, err := keyvault.Open(database, platformSecret, cfg.RecoverySecret, devMode)
	if err != nil {
		logger.Error("mcp: keyvault failed to open", "error", err)
		os.Exit(1)
	}
	cfg.EncryptionKey = vault.DEKHex()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	h, drain := server.BuildHandler(ctx, cfg, database, logger)
	defer drain()

	logger.Info("calnode MCP server ready (stdio transport)")
	if err := h.MCPServer().Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("mcp: stdio server exited with error", "error", err)
		os.Exit(1)
	}
}
