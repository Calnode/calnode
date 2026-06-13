package handler

import (
	"database/sql"
	"log/slog"
)

type Handler struct {
	db     *sql.DB
	logger *slog.Logger
}

func New(db *sql.DB, logger *slog.Logger) *Handler {
	return &Handler{db: db, logger: logger}
}
