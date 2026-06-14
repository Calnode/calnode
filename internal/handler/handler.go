package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/calnode/calnode/internal/booking"
)

type Handler struct {
	db         *sql.DB
	logger     *slog.Logger
	bookingSvc *booking.Service
}

func New(db *sql.DB, logger *slog.Logger) *Handler {
	return &Handler{
		db:         db,
		logger:     logger,
		bookingSvc: booking.New(db),
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}
