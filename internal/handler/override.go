package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

type availOverrideJSON struct {
	ID          string  `json:"id"`
	Date        string  `json:"date"`        // YYYY-MM-DD
	IsAvailable bool    `json:"is_available"`
	StartTime   *string `json:"start_time"`  // HH:MM; only when IsAvailable
	EndTime     *string `json:"end_time"`    // HH:MM; only when IsAvailable
}

// CreateAvailabilityOverride handles POST /v1/availability-overrides.
//
// Body:
//
//	{ "date": "YYYY-MM-DD", "is_available": bool,
//	  "start_time": "HH:MM", "end_time": "HH:MM" }
//
// start_time/end_time are required when is_available=true.
// Only one override per (user, date) is allowed; returns 409 if one already exists.
func (h *Handler) CreateAvailabilityOverride(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Date        string  `json:"date"`
		IsAvailable bool    `json:"is_available"`
		StartTime   *string `json:"start_time"`
		EndTime     *string `json:"end_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Date == "" {
		h.writeError(w, http.StatusBadRequest, "date is required (YYYY-MM-DD)")
		return
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		h.writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if req.IsAvailable {
		if req.StartTime == nil || req.EndTime == nil || *req.StartTime == "" || *req.EndTime == "" {
			h.writeError(w, http.StatusBadRequest, "start_time and end_time are required when is_available is true")
			return
		}
		if !validHHMM(*req.StartTime) || !validHHMM(*req.EndTime) {
			h.writeError(w, http.StatusBadRequest, "start_time and end_time must be HH:MM (e.g. 09:00)")
			return
		}
		if *req.StartTime >= *req.EndTime {
			h.writeError(w, http.StatusBadRequest, "start_time must be before end_time")
			return
		}
	}

	isAvailInt := 0
	if req.IsAvailable {
		isAvailInt = 1
	}
	// Discard times for blocked days — callers might send them but they're meaningless.
	if !req.IsAvailable {
		req.StartTime = nil
		req.EndTime = nil
	}

	id := uid.New()
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO availability_overrides (id, user_id, date, is_available, start_time, end_time)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, user.ID, req.Date, isAvailInt, req.StartTime, req.EndTime); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			h.writeError(w, http.StatusConflict, "an override already exists for this date; delete it first")
			return
		}
		h.logger.ErrorContext(r.Context(), "create availability override", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := availOverrideJSON{
		ID:          id,
		Date:        req.Date,
		IsAvailable: req.IsAvailable,
	}
	if req.IsAvailable {
		resp.StartTime = req.StartTime
		resp.EndTime = req.EndTime
	}
	h.writeJSON(w, http.StatusCreated, resp)
}

// ListAvailabilityOverrides handles GET /v1/availability-overrides.
func (h *Handler) ListAvailabilityOverrides(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, date, is_available,
		       COALESCE(start_time,''), COALESCE(end_time,'')
		FROM availability_overrides
		WHERE user_id = ?
		ORDER BY date`, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list availability overrides", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := make([]availOverrideJSON, 0)
	for rows.Next() {
		var item availOverrideJSON
		var isAvail int
		var startT, endT string
		if err := rows.Scan(&item.ID, &item.Date, &isAvail, &startT, &endT); err != nil {
			h.logger.ErrorContext(r.Context(), "scan availability override", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		item.IsAvailable = isAvail != 0
		if item.IsAvailable && startT != "" {
			item.StartTime = &startT
			item.EndTime = &endT
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list overrides rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// UpdateAvailabilityOverride handles PATCH /v1/availability-overrides/{id}.
func (h *Handler) UpdateAvailabilityOverride(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		IsAvailable *bool   `json:"is_available"`
		StartTime   *string `json:"start_time"`
		EndTime     *string `json:"end_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var current availOverrideJSON
	var isAvail int
	var startT, endT string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, date, is_available, COALESCE(start_time,''), COALESCE(end_time,'')
		 FROM availability_overrides WHERE id = ? AND user_id = ?`, id, user.ID).
		Scan(&current.ID, &current.Date, &isAvail, &startT, &endT)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "override not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update availability override: fetch", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	current.IsAvailable = isAvail != 0
	if current.IsAvailable && startT != "" {
		current.StartTime = &startT
		current.EndTime = &endT
	}

	if req.IsAvailable != nil {
		current.IsAvailable = *req.IsAvailable
	}
	if req.StartTime != nil {
		if !validHHMM(*req.StartTime) {
			h.writeError(w, http.StatusBadRequest, "start_time must be HH:MM (e.g. 09:00)")
			return
		}
		current.StartTime = req.StartTime
	}
	if req.EndTime != nil {
		if !validHHMM(*req.EndTime) {
			h.writeError(w, http.StatusBadRequest, "end_time must be HH:MM (e.g. 09:00)")
			return
		}
		current.EndTime = req.EndTime
	}

	if current.IsAvailable {
		if current.StartTime == nil || current.EndTime == nil || *current.StartTime == "" || *current.EndTime == "" {
			h.writeError(w, http.StatusBadRequest, "start_time and end_time are required when is_available is true")
			return
		}
		if *current.StartTime >= *current.EndTime {
			h.writeError(w, http.StatusBadRequest, "start_time must be before end_time")
			return
		}
	} else {
		current.StartTime = nil
		current.EndTime = nil
	}

	isAvailInt := 0
	if current.IsAvailable {
		isAvailInt = 1
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE availability_overrides SET is_available=?, start_time=?, end_time=? WHERE id=? AND user_id=?`,
		isAvailInt, current.StartTime, current.EndTime, id, user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "update availability override", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, current)
}

// DeleteAvailabilityOverride handles DELETE /v1/availability-overrides/{id}.
func (h *Handler) DeleteAvailabilityOverride(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM availability_overrides WHERE id = ? AND user_id = ?`, id, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete availability override", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusNotFound, "override not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validHHMM returns true if s is a valid "HH:MM" time string.
func validHHMM(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	for _, c := range []byte{s[0], s[1], s[3], s[4]} {
		if c < '0' || c > '9' {
			return false
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	return h <= 23 && m <= 59
}
