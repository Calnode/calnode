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
	Date        string  `json:"date"` // YYYY-MM-DD
	IsAvailable bool    `json:"is_available"`
	Reason      string  `json:"reason"`             // "day_off" | "out_of_office" | "custom_hours"
	StartTime   *string `json:"start_time"`         // HH:MM; only when IsAvailable
	EndTime     *string `json:"end_time"`           // HH:MM; only when IsAvailable
	GroupID     *string `json:"group_id,omitempty"` // set on rows from a multi-day span
}

func validOverrideReason(r string) bool {
	return r == "day_off" || r == "out_of_office" || r == "custom_hours"
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
		Date      string  `json:"date"`
		EndDate   *string `json:"end_date"` // set for a multi-day out-of-office span
		Reason    string  `json:"reason"`
		StartTime *string `json:"start_time"`
		EndTime   *string `json:"end_time"`
		// Legacy field — ignored if reason is provided.
		IsAvailable *bool `json:"is_available"`
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
	// Derive reason: prefer explicit reason field, fall back to is_available for old callers.
	if req.Reason == "" {
		if req.IsAvailable != nil && *req.IsAvailable {
			req.Reason = "custom_hours"
		} else {
			req.Reason = "day_off"
		}
	}
	if !validOverrideReason(req.Reason) {
		h.writeError(w, http.StatusBadRequest, "reason must be 'day_off', 'out_of_office', or 'custom_hours'")
		return
	}
	isCustom := req.Reason == "custom_hours"
	if isCustom {
		if req.StartTime == nil || req.EndTime == nil || *req.StartTime == "" || *req.EndTime == "" {
			h.writeError(w, http.StatusBadRequest, "start_time and end_time are required for custom_hours")
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
	} else {
		req.StartTime = nil
		req.EndTime = nil
	}

	// Date-range block: expand [date … end_date] into one blocked row per date, tied
	// by a shared group_id so the UI shows/deletes them as a single "out of office"
	// span. Slot generation is unchanged (it reads the per-date rows).
	if req.EndDate != nil && *req.EndDate != "" {
		if isCustom {
			h.writeError(w, http.StatusBadRequest, "a date range is only for 'day_off' or 'out_of_office', not custom hours")
			return
		}
		startDate, _ := time.Parse("2006-01-02", req.Date) // req.Date already validated above
		endDate, err := time.Parse("2006-01-02", *req.EndDate)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "end_date must be YYYY-MM-DD")
			return
		}
		if endDate.Before(startDate) {
			h.writeError(w, http.StatusBadRequest, "end_date must be on or after date")
			return
		}
		if endDate.Sub(startDate) > 366*24*time.Hour {
			h.writeError(w, http.StatusBadRequest, "date range is too long (max 366 days)")
			return
		}
		groupID := uid.New()
		tx, err := h.db.BeginTx(r.Context(), nil)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "override range: begin tx", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		defer tx.Rollback() //nolint:errcheck
		days := 0
		for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
			// A range block wins over any existing single-date override on that date.
			if _, err := tx.ExecContext(r.Context(), `
				INSERT INTO availability_overrides (id, user_id, date, is_available, reason, start_time, end_time, group_id)
				VALUES (?, ?, ?, 0, ?, NULL, NULL, ?)
				ON CONFLICT(user_id, date) DO UPDATE SET
					is_available = 0, reason = excluded.reason, start_time = NULL, end_time = NULL, group_id = excluded.group_id`,
				uid.New(), user.ID, d.Format("2006-01-02"), req.Reason, groupID); err != nil {
				h.logger.ErrorContext(r.Context(), "override range: insert", "error", err)
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			days++
		}
		if err := tx.Commit(); err != nil {
			h.logger.ErrorContext(r.Context(), "override range: commit", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.writeJSON(w, http.StatusCreated, map[string]any{
			"group_id": groupID, "reason": req.Reason, "start": req.Date, "end": *req.EndDate, "days": days,
		})
		return
	}

	isAvailInt := 0
	if isCustom {
		isAvailInt = 1
	}

	id := uid.New()
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO availability_overrides (id, user_id, date, is_available, reason, start_time, end_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, user.ID, req.Date, isAvailInt, req.Reason, req.StartTime, req.EndTime); err != nil {
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
		IsAvailable: isCustom,
		Reason:      req.Reason,
	}
	if isCustom {
		resp.StartTime = req.StartTime
		resp.EndTime = req.EndTime
	}
	h.writeJSON(w, http.StatusCreated, resp)
}

// ListAvailabilityOverrides handles GET /v1/availability-overrides.
func (h *Handler) ListAvailabilityOverrides(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, date, is_available, COALESCE(reason,'day_off'),
		       COALESCE(start_time,''), COALESCE(end_time,''), group_id
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
		var groupID sql.NullString
		if err := rows.Scan(&item.ID, &item.Date, &isAvail, &item.Reason, &startT, &endT, &groupID); err != nil {
			h.logger.ErrorContext(r.Context(), "scan availability override", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		item.IsAvailable = isAvail != 0
		if item.IsAvailable && startT != "" {
			item.StartTime = &startT
			item.EndTime = &endT
		}
		if groupID.Valid && groupID.String != "" {
			item.GroupID = &groupID.String
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
		Reason    *string `json:"reason"`
		StartTime *string `json:"start_time"`
		EndTime   *string `json:"end_time"`
		// Legacy field — accepted but reason takes precedence.
		IsAvailable *bool `json:"is_available"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var current availOverrideJSON
	var isAvail int
	var startT, endT string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, date, is_available, COALESCE(reason,'day_off'), COALESCE(start_time,''), COALESCE(end_time,'')
		 FROM availability_overrides WHERE id = ? AND user_id = ?`, id, user.ID).
		Scan(&current.ID, &current.Date, &isAvail, &current.Reason, &startT, &endT)
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

	if req.Reason != nil {
		if !validOverrideReason(*req.Reason) {
			h.writeError(w, http.StatusBadRequest, "reason must be 'day_off', 'out_of_office', or 'custom_hours'")
			return
		}
		current.Reason = *req.Reason
	} else if req.IsAvailable != nil {
		// Legacy fallback.
		if *req.IsAvailable {
			current.Reason = "custom_hours"
		} else if current.Reason == "custom_hours" {
			current.Reason = "day_off"
		}
	}
	current.IsAvailable = current.Reason == "custom_hours"

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
			h.writeError(w, http.StatusBadRequest, "start_time and end_time are required for custom_hours")
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
		`UPDATE availability_overrides SET is_available=?, reason=?, start_time=?, end_time=? WHERE id=? AND user_id=?`,
		isAvailInt, current.Reason, current.StartTime, current.EndTime, id, user.ID); err != nil {
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

// DeleteAvailabilityOverrideGroup handles DELETE /v1/availability-overrides/group/{groupId}
// — removes every per-date row of a multi-day span in one call.
func (h *Handler) DeleteAvailabilityOverrideGroup(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	groupID := r.PathValue("groupId")

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM availability_overrides WHERE group_id = ? AND user_id = ?`, groupID, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete availability override group", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusNotFound, "no overrides found for that group")
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
