package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/uid"
)

type eventTypeJSON struct {
	ID                  string  `json:"id"`
	Slug                string  `json:"slug"`
	Name                string  `json:"name"`
	Description         *string `json:"description"`
	DurationMinutes     int     `json:"duration_minutes"`
	SlotIntervalMinutes int     `json:"slot_interval_minutes"`
	LocationType        string  `json:"location_type"`
	LocationValue       *string `json:"location_value"`
	RoutingMode         string  `json:"routing_mode"`
	BufferBeforeMinutes int     `json:"buffer_before_minutes"`
	BufferAfterMinutes  int     `json:"buffer_after_minutes"`
	MinNoticeMinutes    int     `json:"min_notice_minutes"`
	MaxFutureDays       int     `json:"max_future_days"`
	IsActive            bool    `json:"is_active"`
	IsPublic            bool    `json:"is_public"`
	CreatedAt           string  `json:"created_at"`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEventType(s rowScanner) (*eventTypeJSON, error) {
	var et eventTypeJSON
	var desc, locVal sql.NullString
	var isActive, isPublic int

	err := s.Scan(
		&et.ID, &et.Slug, &et.Name, &desc,
		&et.DurationMinutes, &et.SlotIntervalMinutes,
		&et.LocationType, &locVal,
		&et.RoutingMode,
		&et.BufferBeforeMinutes, &et.BufferAfterMinutes,
		&et.MinNoticeMinutes, &et.MaxFutureDays,
		&isActive, &isPublic, &et.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		et.Description = &desc.String
	}
	if locVal.Valid {
		et.LocationValue = &locVal.String
	}
	et.IsActive = isActive != 0
	et.IsPublic = isPublic != 0
	return &et, nil
}

const selectETCols = `SELECT id, slug, name, description,
	duration_minutes, slot_interval_minutes,
	location_type, location_value,
	routing_mode,
	buffer_before_minutes, buffer_after_minutes,
	min_notice_minutes, max_future_days,
	is_active, is_public, created_at
FROM event_types`

// CreateEventType handles POST /v1/event-types.
func (h *Handler) CreateEventType(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Slug                string  `json:"slug"`
		Name                string  `json:"name"`
		Description         *string `json:"description"`
		DurationMinutes     int     `json:"duration_minutes"`
		SlotIntervalMinutes *int    `json:"slot_interval_minutes"`
		LocationType        *string `json:"location_type"`
		LocationValue       *string `json:"location_value"`
		RoutingMode         *string `json:"routing_mode"`
		BufferBeforeMinutes *int    `json:"buffer_before_minutes"`
		BufferAfterMinutes  *int    `json:"buffer_after_minutes"`
		MinNoticeMinutes    *int    `json:"min_notice_minutes"`
		MaxFutureDays       *int    `json:"max_future_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Slug == "" || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "slug and name are required")
		return
	}
	if req.DurationMinutes <= 0 {
		h.writeError(w, http.StatusBadRequest, "duration_minutes must be positive")
		return
	}

	slotInterval := 30
	if req.SlotIntervalMinutes != nil {
		slotInterval = *req.SlotIntervalMinutes
	}
	locType := "link"
	if req.LocationType != nil {
		locType = *req.LocationType
	}
	routingMode := "fixed"
	if req.RoutingMode != nil {
		routingMode = *req.RoutingMode
	}
	bufBefore, bufAfter, minNotice, maxFuture := 0, 0, 0, 60
	if req.BufferBeforeMinutes != nil {
		bufBefore = *req.BufferBeforeMinutes
	}
	if req.BufferAfterMinutes != nil {
		bufAfter = *req.BufferAfterMinutes
	}
	if req.MinNoticeMinutes != nil {
		minNotice = *req.MinNoticeMinutes
	}
	if req.MaxFutureDays != nil {
		maxFuture = *req.MaxFutureDays
	}

	id := uid.New()
	_, err := h.db.ExecContext(r.Context(), `
		INSERT INTO event_types
		  (id, user_id, slug, name, description, duration_minutes,
		   slot_interval_minutes, location_type, location_value,
		   routing_mode, buffer_before_minutes, buffer_after_minutes,
		   min_notice_minutes, max_future_days)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, user.ID, req.Slug, req.Name, req.Description,
		req.DurationMinutes, slotInterval, locType, req.LocationValue,
		routingMode, bufBefore, bufAfter, minNotice, maxFuture)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			h.writeError(w, http.StatusConflict, "slug already in use")
			return
		}
		if strings.Contains(err.Error(), "CHECK constraint failed") {
			h.writeError(w, http.StatusBadRequest, "invalid location_type or routing_mode value")
			return
		}
		h.logger.ErrorContext(r.Context(), "create event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	row := h.db.QueryRowContext(r.Context(), selectETCols+" WHERE id = ?", id)
	et, err := scanEventType(row)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "fetch created event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusCreated, et)
}

// ListEventTypes handles GET /v1/event-types.
func (h *Handler) ListEventTypes(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	rows, err := h.db.QueryContext(r.Context(),
		selectETCols+" WHERE user_id = ? ORDER BY created_at", user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list event types", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := make([]eventTypeJSON, 0)
	for rows.Next() {
		et, err := scanEventType(rows)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "scan event type", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, *et)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list event types rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GetEventType handles GET /v1/event-types/{slug}.
func (h *Handler) GetEventType(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")

	row := h.db.QueryRowContext(r.Context(),
		selectETCols+" WHERE slug = ? AND user_id = ?", slug, user.ID)
	et, err := scanEventType(row)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if et == nil {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	h.writeJSON(w, http.StatusOK, et)
}

// PatchEventType handles PATCH /v1/event-types/{slug}.
func (h *Handler) PatchEventType(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Name                *string `json:"name"`
		Description         *string `json:"description"`
		DurationMinutes     *int    `json:"duration_minutes"`
		SlotIntervalMinutes *int    `json:"slot_interval_minutes"`
		LocationType        *string `json:"location_type"`
		LocationValue       *string `json:"location_value"`
		RoutingMode         *string `json:"routing_mode"`
		BufferBeforeMinutes *int    `json:"buffer_before_minutes"`
		BufferAfterMinutes  *int    `json:"buffer_after_minutes"`
		MinNoticeMinutes    *int    `json:"min_notice_minutes"`
		MaxFutureDays       *int    `json:"max_future_days"`
		IsActive            *bool   `json:"is_active"`
		IsPublic            *bool   `json:"is_public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var setClauses []string
	var args []any
	set := func(col string, val any) {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}

	if req.Name != nil {
		if *req.Name == "" {
			h.writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		set("name", *req.Name)
	}
	if req.Description != nil {
		set("description", *req.Description)
	}
	if req.DurationMinutes != nil {
		if *req.DurationMinutes <= 0 {
			h.writeError(w, http.StatusBadRequest, "duration_minutes must be positive")
			return
		}
		set("duration_minutes", *req.DurationMinutes)
	}
	if req.SlotIntervalMinutes != nil {
		set("slot_interval_minutes", *req.SlotIntervalMinutes)
	}
	if req.LocationType != nil {
		set("location_type", *req.LocationType)
	}
	if req.LocationValue != nil {
		set("location_value", *req.LocationValue)
	}
	if req.RoutingMode != nil {
		set("routing_mode", *req.RoutingMode)
	}
	if req.BufferBeforeMinutes != nil {
		set("buffer_before_minutes", *req.BufferBeforeMinutes)
	}
	if req.BufferAfterMinutes != nil {
		set("buffer_after_minutes", *req.BufferAfterMinutes)
	}
	if req.MinNoticeMinutes != nil {
		set("min_notice_minutes", *req.MinNoticeMinutes)
	}
	if req.MaxFutureDays != nil {
		set("max_future_days", *req.MaxFutureDays)
	}
	if req.IsActive != nil {
		v := 0
		if *req.IsActive {
			v = 1
		}
		set("is_active", v)
	}
	if req.IsPublic != nil {
		v := 0
		if *req.IsPublic {
			v = 1
		}
		set("is_public", v)
	}

	if len(setClauses) == 0 {
		h.writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	args = append(args, slug, user.ID)
	res, err := h.db.ExecContext(r.Context(),
		"UPDATE event_types SET "+strings.Join(setClauses, ", ")+" WHERE slug = ? AND user_id = ?",
		args...)
	if err != nil {
		if strings.Contains(err.Error(), "CHECK constraint failed") {
			h.writeError(w, http.StatusBadRequest, "invalid location_type or routing_mode value")
			return
		}
		h.logger.ErrorContext(r.Context(), "patch event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}

	row := h.db.QueryRowContext(r.Context(),
		selectETCols+" WHERE slug = ? AND user_id = ?", slug, user.ID)
	et, err := scanEventType(row)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "fetch patched event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, et)
}

// DeleteEventType handles DELETE /v1/event-types/{slug}.
func (h *Handler) DeleteEventType(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM event_types WHERE slug = ? AND user_id = ?`, slug, user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			h.writeError(w, http.StatusConflict, "event type has existing bookings")
			return
		}
		h.logger.ErrorContext(r.Context(), "delete event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
