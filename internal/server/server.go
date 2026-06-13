package server

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/handler"
)

func New(cfg *config.Config, db *sql.DB, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	h := handler.New(db, logger)

	// Ops endpoints (§16)
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)

	return RequestID(Logging(logger, mux))
}
