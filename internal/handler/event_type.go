package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/uid"
)

const (
	defaultMsgConfirmation = "Looking forward to our meeting! Feel free to reply if you have any questions beforehand."
	defaultMsgCancellation = "Apologies for the cancellation. You're welcome to rebook at any time."
	defaultMsgReschedule   = "Apologies for the change — looking forward to connecting at the new time!"
	defaultMsgReminder     = "Please reach out if you need to make any last-minute changes."
)

type eventTypeJSON struct {
	ID                  string   `json:"id"`
	Slug                string   `json:"slug"`
	Name                string   `json:"name"`
	Description         *string  `json:"description"`
	DurationMinutes     int      `json:"duration_minutes"`
	SlotIntervalMinutes int      `json:"slot_interval_minutes"`
	LocationType        string   `json:"location_type"`
	LocationValue       *string  `json:"location_value"`
	RoutingMode         string   `json:"routing_mode"`
	RRStrategy          string   `json:"rr_strategy"`
	BufferBeforeMinutes int      `json:"buffer_before_minutes"`
	BufferAfterMinutes  int      `json:"buffer_after_minutes"`
	MinNoticeMinutes    int      `json:"min_notice_minutes"`
	MaxFutureDays       int      `json:"max_future_days"`
	MaxActiveBookings   int      `json:"max_active_bookings"`
	IsActive            bool     `json:"is_active"`
	IsPublic            bool     `json:"is_public"`
	CreatedAt           string   `json:"created_at"`
	MsgConfirmation     *string  `json:"msg_confirmation"`
	MsgCancellation     *string  `json:"msg_cancellation"`
	MsgReschedule       *string  `json:"msg_reschedule"`
	MsgReminder         *string  `json:"msg_reminder"`
	Reminders           []int    `json:"reminders"` // hours_before values
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEventType(s rowScanner) (*eventTypeJSON, error) {
	var et eventTypeJSON
	var desc, locVal, msgConf, msgCancel, msgResched, msgRemind sql.NullString
	var isActive, isPublic int

	err := s.Scan(
		&et.ID, &et.Slug, &et.Name, &desc,
		&et.DurationMinutes, &et.SlotIntervalMinutes,
		&et.LocationType, &locVal,
		&et.RoutingMode, &et.RRStrategy,
		&et.BufferBeforeMinutes, &et.BufferAfterMinutes,
		&et.MinNoticeMinutes, &et.MaxFutureDays, &et.MaxActiveBookings,
		&isActive, &isPublic, &et.CreatedAt,
		&msgConf, &msgCancel, &msgResched, &msgRemind,
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
	if msgConf.Valid {
		et.MsgConfirmation = &msgConf.String
	}
	if msgCancel.Valid {
		et.MsgCancellation = &msgCancel.String
	}
	if msgResched.Valid {
		et.MsgReschedule = &msgResched.String
	}
	if msgRemind.Valid {
		et.MsgReminder = &msgRemind.String
	}
	et.Reminders = []int{} // initialise to non-nil so JSON encodes as [] not null
	return &et, nil
}

const selectETCols = `SELECT id, slug, name, description,
	duration_minutes, slot_interval_minutes,
	location_type, location_value,
	routing_mode, rr_strategy,
	buffer_before_minutes, buffer_after_minutes,
	min_notice_minutes, max_future_days, max_active_bookings,
	is_active, is_public, created_at,
	msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder
FROM event_types`

// loadReminders fetches the hours_before list for an event type and sets et.Reminders.
func (h *Handler) loadReminders(ctx context.Context, etID string, et *eventTypeJSON) error {
	rows, err := h.db.QueryContext(ctx,
		`SELECT hours_before FROM event_type_reminders WHERE event_type_id = ? ORDER BY hours_before DESC`, etID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hb int
		if err := rows.Scan(&hb); err != nil {
			return err
		}
		et.Reminders = append(et.Reminders, hb)
	}
	return rows.Err()
}

// validReminderHours is the allowed set of hours_before values.
var validReminderHours = map[int]bool{1: true, 2: true, 4: true, 8: true, 12: true, 24: true, 48: true, 72: true, 168: true}

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
		MaxActiveBookings   *int    `json:"max_active_bookings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Slug == "" || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "slug and name are required")
		return
	}
	if req.MaxActiveBookings != nil && *req.MaxActiveBookings < 0 {
		h.writeError(w, http.StatusBadRequest, "max_active_bookings cannot be negative (0 = unlimited)")
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
	maxActive := 1
	if req.MaxActiveBookings != nil {
		maxActive = *req.MaxActiveBookings
	}

	id := uid.New()
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create event type: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO event_types
		  (id, user_id, slug, name, description, duration_minutes,
		   slot_interval_minutes, location_type, location_value,
		   routing_mode, buffer_before_minutes, buffer_after_minutes,
		   min_notice_minutes, max_future_days, max_active_bookings,
		   msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, user.ID, req.Slug, req.Name, req.Description,
		req.DurationMinutes, slotInterval, locType, req.LocationValue,
		routingMode, bufBefore, bufAfter, minNotice, maxFuture, maxActive,
		defaultMsgConfirmation, defaultMsgCancellation, defaultMsgReschedule, defaultMsgReminder)
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

	// Seed the owner as the single required host (Normal). Keeps host resolution
	// uniform — every event type has at least one host from creation.
	if _, err = tx.ExecContext(r.Context(), `
		INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
		VALUES (?, ?, ?, 'required', 0)`, uid.New(), id, user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "create event type: seed host", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err = tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "create event type: commit", "error", err)
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
	if err := h.loadReminders(r.Context(), et.ID, et); err != nil {
		h.logger.ErrorContext(r.Context(), "get event type: load reminders", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
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
		Name                *string  `json:"name"`
		Description         *string  `json:"description"`
		DurationMinutes     *int     `json:"duration_minutes"`
		SlotIntervalMinutes *int     `json:"slot_interval_minutes"`
		LocationType        *string  `json:"location_type"`
		LocationValue       *string  `json:"location_value"`
		RoutingMode         *string  `json:"routing_mode"`
		RRStrategy          *string  `json:"rr_strategy"`
		BufferBeforeMinutes *int     `json:"buffer_before_minutes"`
		BufferAfterMinutes  *int     `json:"buffer_after_minutes"`
		MinNoticeMinutes    *int     `json:"min_notice_minutes"`
		MaxFutureDays       *int     `json:"max_future_days"`
		MaxActiveBookings   *int     `json:"max_active_bookings"`
		IsActive            *bool    `json:"is_active"`
		IsPublic            *bool    `json:"is_public"`
		MsgConfirmation     *string  `json:"msg_confirmation"`
		MsgCancellation     *string  `json:"msg_cancellation"`
		MsgReschedule       *string  `json:"msg_reschedule"`
		MsgReminder         *string  `json:"msg_reminder"`
		Reminders           []int    `json:"reminders"` // nil = don't touch; [] = clear all
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate reminders list before touching the DB.
	if req.Reminders != nil {
		for _, hb := range req.Reminders {
			if !validReminderHours[hb] {
				h.writeError(w, http.StatusBadRequest, "reminder hours_before must be one of: 1, 2, 4, 8, 12, 24, 48, 72, 168")
				return
			}
		}
		if len(req.Reminders) > 5 {
			h.writeError(w, http.StatusBadRequest, "at most 5 reminders per event type")
			return
		}
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
		switch *req.RoutingMode {
		case "fixed", "round_robin", "collective":
			set("routing_mode", *req.RoutingMode)
		default:
			h.writeError(w, http.StatusBadRequest, "routing_mode must be 'fixed', 'round_robin', or 'collective'")
			return
		}
	}
	if req.RRStrategy != nil {
		switch *req.RRStrategy {
		case "even", "soonest", "priority":
			set("rr_strategy", *req.RRStrategy)
		default:
			h.writeError(w, http.StatusBadRequest, "rr_strategy must be 'even', 'soonest', or 'priority'")
			return
		}
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
	if req.MaxActiveBookings != nil {
		if *req.MaxActiveBookings < 0 {
			h.writeError(w, http.StatusBadRequest, "max_active_bookings cannot be negative (0 = unlimited)")
			return
		}
		set("max_active_bookings", *req.MaxActiveBookings)
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
	const maxMsgLen = 2000
	if req.MsgConfirmation != nil {
		if len(*req.MsgConfirmation) > maxMsgLen {
			h.writeError(w, http.StatusBadRequest, "msg_confirmation exceeds 2000 characters")
			return
		}
		set("msg_confirmation", nullableString(*req.MsgConfirmation))
	}
	if req.MsgCancellation != nil {
		if len(*req.MsgCancellation) > maxMsgLen {
			h.writeError(w, http.StatusBadRequest, "msg_cancellation exceeds 2000 characters")
			return
		}
		set("msg_cancellation", nullableString(*req.MsgCancellation))
	}
	if req.MsgReschedule != nil {
		if len(*req.MsgReschedule) > maxMsgLen {
			h.writeError(w, http.StatusBadRequest, "msg_reschedule exceeds 2000 characters")
			return
		}
		set("msg_reschedule", nullableString(*req.MsgReschedule))
	}
	if req.MsgReminder != nil {
		if len(*req.MsgReminder) > maxMsgLen {
			h.writeError(w, http.StatusBadRequest, "msg_reminder exceeds 2000 characters")
			return
		}
		set("msg_reminder", nullableString(*req.MsgReminder))
	}

	// Look up the event type ID (needed for reminder upsert and to verify ownership).
	var etID string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM event_types WHERE slug = ? AND user_id = ?`, slug, user.ID).
		Scan(&etID); err != nil {
		if err == sql.ErrNoRows {
			h.writeError(w, http.StatusNotFound, "event type not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "patch event type: lookup id", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Apply the event_types UPDATE if there are scalar fields to change.
	if len(setClauses) > 0 {
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
	}

	// Replace the reminders list atomically if the caller sent it.
	if req.Reminders != nil {
		if err := h.replaceReminders(r.Context(), etID, req.Reminders); err != nil {
			h.logger.ErrorContext(r.Context(), "patch event type: replace reminders", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	row := h.db.QueryRowContext(r.Context(),
		selectETCols+" WHERE slug = ? AND user_id = ?", slug, user.ID)
	et, err := scanEventType(row)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "fetch patched event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.loadReminders(r.Context(), etID, et); err != nil {
		h.logger.ErrorContext(r.Context(), "fetch patched event type: load reminders", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, et)
}

// nullableString converts an empty string to nil (NULL in SQLite) so clearing a
// custom note stores NULL rather than an empty string.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// replaceReminders deletes all existing reminder rows for etID and inserts newHours.
func (h *Handler) replaceReminders(ctx context.Context, etID string, newHours []int) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM event_type_reminders WHERE event_type_id = ?`, etID); err != nil {
		return err
	}
	for _, hb := range newHours {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO event_type_reminders (id, event_type_id, hours_before) VALUES (?, ?, ?)`,
			uid.New(), etID, hb); err != nil {
			return err
		}
	}
	return tx.Commit()
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
