package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/uid"
)

type availRuleJSON struct {
	ID          string  `json:"id"`
	EventTypeID *string `json:"event_type_id"`
	DayOfWeek   int     `json:"day_of_week"`
	StartTime   string  `json:"start_time"`
	EndTime     string  `json:"end_time"`
}

// CreateAvailabilityRule handles POST /v1/availability-rules.
func (h *Handler) CreateAvailabilityRule(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		EventTypeID *string `json:"event_type_id"`
		DayOfWeek   int     `json:"day_of_week"`
		StartTime   string  `json:"start_time"`
		EndTime     string  `json:"end_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.StartTime == "" || req.EndTime == "" {
		h.writeError(w, http.StatusBadRequest, "start_time and end_time are required (HH:MM)")
		return
	}
	if req.DayOfWeek < 0 || req.DayOfWeek > 6 {
		h.writeError(w, http.StatusBadRequest, "day_of_week must be 0 (Sun) through 6 (Sat)")
		return
	}

	id := uid.New()
	_, err := h.db.ExecContext(r.Context(), `
		INSERT INTO availability_rules (id, user_id, event_type_id, day_of_week, start_time, end_time)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, user.ID, req.EventTypeID, req.DayOfWeek, req.StartTime, req.EndTime)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			h.writeError(w, http.StatusConflict, "a rule for this day and time already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "create availability rule", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, availRuleJSON{
		ID:          id,
		EventTypeID: req.EventTypeID,
		DayOfWeek:   req.DayOfWeek,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
	})
}

// ListAvailabilityRules handles GET /v1/availability-rules.
// Optional query param: ?event_type_id=<id> to filter; omit for global rules.
func (h *Handler) ListAvailabilityRules(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	var (
		rows *sql.Rows
		err  error
	)
	if etID := r.URL.Query().Get("event_type_id"); etID != "" {
		rows, err = h.db.QueryContext(r.Context(), `
			SELECT id, event_type_id, day_of_week, start_time, end_time
			FROM availability_rules
			WHERE user_id = ? AND event_type_id = ?
			ORDER BY day_of_week, start_time`, user.ID, etID)
	} else {
		rows, err = h.db.QueryContext(r.Context(), `
			SELECT id, event_type_id, day_of_week, start_time, end_time
			FROM availability_rules
			WHERE user_id = ?
			ORDER BY day_of_week, start_time`, user.ID)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list availability rules", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := make([]availRuleJSON, 0)
	for rows.Next() {
		var rule availRuleJSON
		var etID sql.NullString
		if err := rows.Scan(&rule.ID, &etID, &rule.DayOfWeek, &rule.StartTime, &rule.EndTime); err != nil {
			h.logger.ErrorContext(r.Context(), "scan availability rule", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if etID.Valid {
			rule.EventTypeID = &etID.String
		}
		items = append(items, rule)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list availability rules rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// UpdateAvailabilityRule handles PATCH /v1/availability-rules/{id}.
func (h *Handler) UpdateAvailabilityRule(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		DayOfWeek *int    `json:"day_of_week"`
		StartTime *string `json:"start_time"`
		EndTime   *string `json:"end_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.DayOfWeek != nil && (*req.DayOfWeek < 0 || *req.DayOfWeek > 6) {
		h.writeError(w, http.StatusBadRequest, "day_of_week must be 0 (Sun) through 6 (Sat)")
		return
	}

	var current availRuleJSON
	var etID sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, event_type_id, day_of_week, start_time, end_time
		 FROM availability_rules WHERE id = ? AND user_id = ?`, id, user.ID).
		Scan(&current.ID, &etID, &current.DayOfWeek, &current.StartTime, &current.EndTime)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update availability rule: fetch", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if etID.Valid {
		current.EventTypeID = &etID.String
	}

	if req.DayOfWeek != nil {
		current.DayOfWeek = *req.DayOfWeek
	}
	if req.StartTime != nil {
		current.StartTime = *req.StartTime
	}
	if req.EndTime != nil {
		current.EndTime = *req.EndTime
	}
	if !validHHMM(current.StartTime) || !validHHMM(current.EndTime) {
		h.writeError(w, http.StatusBadRequest, "start_time and end_time must be HH:MM (e.g. 09:00)")
		return
	}
	if current.StartTime >= current.EndTime {
		h.writeError(w, http.StatusBadRequest, "end_time must be after start_time")
		return
	}

	_, err = h.db.ExecContext(r.Context(),
		`UPDATE availability_rules SET day_of_week=?, start_time=?, end_time=? WHERE id=? AND user_id=?`,
		current.DayOfWeek, current.StartTime, current.EndTime, id, user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			h.writeError(w, http.StatusConflict, "a rule for this day and time already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "update availability rule", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, current)
}

// DeleteAvailabilityRule handles DELETE /v1/availability-rules/{id}.
func (h *Handler) DeleteAvailabilityRule(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM availability_rules WHERE id = ? AND user_id = ?`, id, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete availability rule", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
