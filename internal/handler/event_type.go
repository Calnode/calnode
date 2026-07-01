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
	ID                  string  `json:"id"`
	Slug                string  `json:"slug"`
	Name                string  `json:"name"`
	Description         *string `json:"description"`
	DurationMinutes     int     `json:"duration_minutes"`
	SlotIntervalMinutes int     `json:"slot_interval_minutes"`
	LocationType        string  `json:"location_type"`
	LocationValue       *string `json:"location_value"`
	RoutingMode         string  `json:"routing_mode"`
	RRStrategy          string  `json:"rr_strategy"`
	BufferBeforeMinutes int     `json:"buffer_before_minutes"`
	BufferAfterMinutes  int     `json:"buffer_after_minutes"`
	MinNoticeMinutes    int     `json:"min_notice_minutes"`
	MaxFutureDays       int     `json:"max_future_days"`
	MaxActiveBookings   int     `json:"max_active_bookings"`
	IsActive            bool    `json:"is_active"`
	IsPublic            bool    `json:"is_public"`
	CreatedAt           string  `json:"created_at"`
	MsgConfirmation     *string `json:"msg_confirmation"`
	MsgCancellation     *string `json:"msg_cancellation"`
	MsgReschedule       *string `json:"msg_reschedule"`
	MsgReminder         *string `json:"msg_reminder"`
	SubjConfirmation    *string `json:"subj_confirmation"`
	SubjCancellation    *string `json:"subj_cancellation"`
	SubjReschedule      *string `json:"subj_reschedule"`
	SubjReminder        *string `json:"subj_reminder"`
	PriceCents          int     `json:"price_cents"` // 0 = free
	Currency            string  `json:"currency"`    // ISO 4217, lowercase (e.g. "usd")
	Reminders           []int   `json:"reminders"`   // hours_before values
	// Owned is true when the requesting user owns this event type; false when they
	// only see it as an assigned host (read-only — only the owner can edit).
	Owned bool `json:"owned"`
	// OwnerName/OwnerEmail identify the owner so a read-only host knows who to
	// contact for changes. Populated only for the host (read-only) GET case.
	OwnerName  string `json:"owner_name,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEventType(s rowScanner) (*eventTypeJSON, error) {
	return scanEventTypeRow(s)
}

// scanEventTypeRow scans the base event-type columns, plus any `trailing` dest
// pointers (e.g. the computed `owned` column from the list/get queries).
func scanEventTypeRow(s rowScanner, trailing ...any) (*eventTypeJSON, error) {
	var et eventTypeJSON
	var desc, locVal, msgConf, msgCancel, msgResched, msgRemind sql.NullString
	var subjConf, subjCancel, subjResched, subjRemind sql.NullString
	var isActive, isPublic int

	dests := []any{
		&et.ID, &et.Slug, &et.Name, &desc,
		&et.DurationMinutes, &et.SlotIntervalMinutes,
		&et.LocationType, &locVal,
		&et.RoutingMode, &et.RRStrategy,
		&et.BufferBeforeMinutes, &et.BufferAfterMinutes,
		&et.MinNoticeMinutes, &et.MaxFutureDays, &et.MaxActiveBookings,
		&isActive, &isPublic, &et.CreatedAt,
		&msgConf, &msgCancel, &msgResched, &msgRemind,
		&subjConf, &subjCancel, &subjResched, &subjRemind,
		&et.PriceCents, &et.Currency,
	}
	dests = append(dests, trailing...)
	err := s.Scan(dests...)
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
	if subjConf.Valid {
		et.SubjConfirmation = &subjConf.String
	}
	if subjCancel.Valid {
		et.SubjCancellation = &subjCancel.String
	}
	if subjResched.Valid {
		et.SubjReschedule = &subjResched.String
	}
	if subjRemind.Valid {
		et.SubjReminder = &subjRemind.String
	}
	et.Reminders = []int{} // initialise to non-nil so JSON encodes as [] not null
	return &et, nil
}

const etColumns = `id, slug, name, description,
	duration_minutes, slot_interval_minutes,
	location_type, location_value,
	routing_mode, rr_strategy,
	buffer_before_minutes, buffer_after_minutes,
	min_notice_minutes, max_future_days, max_active_bookings,
	is_active, is_public, created_at,
	msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder,
	subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder,
	price_cents, currency`

// selectETCols fetches a single owner-scoped event type (no `owned` column).
const selectETCols = "SELECT " + etColumns + " FROM event_types"

// listEventTypesQuery returns every event type the user owns OR is an assigned
// host on, with an `owned` flag (the owner is also seeded into event_type_hosts,
// so ownership is keyed on event_types.user_id, not host membership).
const listEventTypesQuery = "SELECT " + etColumns + `, (user_id = ?) AS owned
FROM event_types
WHERE user_id = ?
   OR id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?)
ORDER BY created_at`

// getEventTypeQuery fetches one event type by slug if the user owns it or hosts
// it, with the `owned` flag and the owner's name/email (so a read-only host knows
// who to contact for changes).
const getEventTypeQuery = "SELECT " + etColumns + `, (user_id = ?) AS owned,
	(SELECT name FROM users WHERE id = event_types.user_id) AS owner_name,
	(SELECT email FROM users WHERE id = event_types.user_id) AS owner_email
FROM event_types
WHERE slug = ? AND (user_id = ? OR id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`

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
	// Location is usually omitted at create (the quick-create form only sets
	// slug/name/duration) and configured later in the editor. When omitted, pick a
	// smart default from the owner's connected calendar; only validate the location
	// when the caller explicitly set it.
	var locType string
	if req.LocationType != nil {
		locType = *req.LocationType
		if err := h.validateLocation(r.Context(), user.ID, locType, req.LocationValue); err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		locType = h.smartDefaultLocation(r.Context(), user.ID)
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
	et.Owned = true // the creator owns it
	h.writeJSON(w, http.StatusCreated, et)
}

// ListEventTypes handles GET /v1/event-types.
func (h *Handler) ListEventTypes(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	rows, err := h.db.QueryContext(r.Context(), listEventTypesQuery, user.ID, user.ID, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list event types", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := make([]eventTypeJSON, 0)
	for rows.Next() {
		var owned int
		et, err := scanEventTypeRow(rows, &owned)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "scan event type", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		et.Owned = owned != 0
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

	var owned int
	var ownerName, ownerEmail string
	row := h.db.QueryRowContext(r.Context(), getEventTypeQuery, user.ID, slug, user.ID, user.ID)
	et, err := scanEventTypeRow(row, &owned, &ownerName, &ownerEmail)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if et == nil {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	et.Owned = owned != 0
	if !et.Owned { // read-only host: surface who to contact for changes
		et.OwnerName = ownerName
		et.OwnerEmail = ownerEmail
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
		Name                *string `json:"name"`
		Description         *string `json:"description"`
		DurationMinutes     *int    `json:"duration_minutes"`
		SlotIntervalMinutes *int    `json:"slot_interval_minutes"`
		LocationType        *string `json:"location_type"`
		LocationValue       *string `json:"location_value"`
		RoutingMode         *string `json:"routing_mode"`
		RRStrategy          *string `json:"rr_strategy"`
		BufferBeforeMinutes *int    `json:"buffer_before_minutes"`
		BufferAfterMinutes  *int    `json:"buffer_after_minutes"`
		MinNoticeMinutes    *int    `json:"min_notice_minutes"`
		MaxFutureDays       *int    `json:"max_future_days"`
		MaxActiveBookings   *int    `json:"max_active_bookings"`
		IsActive            *bool   `json:"is_active"`
		IsPublic            *bool   `json:"is_public"`
		MsgConfirmation     *string `json:"msg_confirmation"`
		MsgCancellation     *string `json:"msg_cancellation"`
		MsgReschedule       *string `json:"msg_reschedule"`
		MsgReminder         *string `json:"msg_reminder"`
		SubjConfirmation    *string `json:"subj_confirmation"`
		SubjCancellation    *string `json:"subj_cancellation"`
		SubjReschedule      *string `json:"subj_reschedule"`
		SubjReminder        *string `json:"subj_reminder"`
		PriceCents          *int    `json:"price_cents"`
		Currency            *string `json:"currency"`
		Reminders           []int   `json:"reminders"` // nil = don't touch; [] = clear all
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
	if req.PriceCents != nil {
		if *req.PriceCents < 0 {
			h.writeError(w, http.StatusBadRequest, "price_cents cannot be negative (0 = free)")
			return
		}
		// A price is only bookable once payments are configured — block the footgun of a
		// paid event type that no one can actually book.
		if *req.PriceCents > 0 && h.getStripe() == nil {
			h.writeError(w, http.StatusBadRequest, "connect Stripe in Settings → Payments before setting a price")
			return
		}
		set("price_cents", *req.PriceCents)
	}
	if req.Currency != nil {
		cur := strings.ToLower(strings.TrimSpace(*req.Currency))
		if len(cur) != 3 {
			h.writeError(w, http.StatusBadRequest, "currency must be a 3-letter ISO 4217 code")
			return
		}
		set("currency", cur)
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
	const maxSubjLen = 200
	for _, s := range []struct {
		col string
		val *string
	}{
		{"subj_confirmation", req.SubjConfirmation},
		{"subj_cancellation", req.SubjCancellation},
		{"subj_reschedule", req.SubjReschedule},
		{"subj_reminder", req.SubjReminder},
	} {
		if s.val == nil {
			continue
		}
		if len(*s.val) > maxSubjLen {
			h.writeError(w, http.StatusBadRequest, s.col+" exceeds 200 characters")
			return
		}
		set(s.col, nullableString(strings.TrimSpace(*s.val)))
	}

	// Look up the event type ID + current location (needed for reminder upsert, to
	// verify ownership, and to validate the effective online-meeting location below).
	var etID, curLocType, curLocVal string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT id, location_type, COALESCE(location_value, '') FROM event_types WHERE slug = ? AND user_id = ?`, slug, user.ID).
		Scan(&etID, &curLocType, &curLocVal); err != nil {
		if err == sql.ErrNoRows {
			h.writeError(w, http.StatusNotFound, "event type not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "patch event type: lookup id", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Validate the location only when this patch actually touches it (so editing an
	// unrelated field on a legacy event type can't trip a newer rule). When it does,
	// validate the value that WILL be in effect (existing value for the field that
	// isn't changing).
	if req.LocationType != nil || req.LocationValue != nil {
		effLocType := curLocType
		if req.LocationType != nil {
			effLocType = *req.LocationType
		}
		effLocVal := curLocVal
		if req.LocationValue != nil {
			effLocVal = *req.LocationValue
		}
		if err := h.validateLocation(r.Context(), user.ID, effLocType, &effLocVal); err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Apply the event_types UPDATE if there are scalar fields to change.
	if len(setClauses) > 0 {
		args = append(args, slug, user.ID)
		res, err := h.db.ExecContext(r.Context(),
			"UPDATE event_types SET "+strings.Join(setClauses, ", ")+" WHERE slug = ? AND user_id = ?", // #nosec G202 -- setClauses is built by set()/the literal col list above; every column name is a hardcoded string, every value is bound via args...
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
	et.Owned = true // only the owner can patch
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
