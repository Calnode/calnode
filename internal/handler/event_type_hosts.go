package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/calnode/calnode/internal/uid"
)

// EventHost is a resolved host of an event type (used by slot generation and
// booking-time assignment). Archived users are excluded by resolveEventTypeHosts.
type EventHost struct {
	UserID   string
	Role     string // required | rotation | optional
	Priority int
}

// resolveEventTypeHosts returns an event type's active (non-archived) hosts.
// Archived members are skipped so they're never offered or assigned.
func (h *Handler) resolveEventTypeHosts(ctx context.Context, eventTypeID string) ([]EventHost, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT eth.user_id, eth.role, eth.priority
		FROM event_type_hosts eth
		JOIN users u ON u.id = eth.user_id
		WHERE eth.event_type_id = ? AND u.archived_at IS NULL
		ORDER BY eth.priority ASC, eth.user_id ASC`, eventTypeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventHost
	for rows.Next() {
		var e EventHost
		if err := rows.Scan(&e.UserID, &e.Role, &e.Priority); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// eventTypeIDForOwner resolves an event type's id from its slug, scoped to the
// authenticated owner. Returns "" (and writes the response) if not found.
func (h *Handler) eventTypeIDForOwner(w http.ResponseWriter, r *http.Request, slug, userID string) string {
	var id string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM event_types WHERE slug = ? AND user_id = ?`, slug, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return ""
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "event type hosts: resolve id", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return ""
	}
	return id
}

// ListEventTypeHosts handles GET /v1/event-types/{slug}/hosts.
func (h *Handler) ListEventTypeHosts(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	etID := h.eventTypeIDForOwner(w, r, r.PathValue("slug"), user.ID)
	if etID == "" {
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT eth.user_id, u.name, u.email, COALESCE(u.avatar_url,''), eth.role, eth.priority, u.archived_at
		FROM event_type_hosts eth
		JOIN users u ON u.id = eth.user_id
		WHERE eth.event_type_id = ?
		ORDER BY eth.priority ASC, u.name ASC`, etID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list event type hosts", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type hostRow struct {
		UserID    string `json:"user_id"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url,omitempty"`
		Role      string `json:"role"`
		Priority  int    `json:"priority"`
		Archived  bool   `json:"archived"`
	}
	out := []hostRow{}
	for rows.Next() {
		var hr hostRow
		var archivedAt sql.NullString
		if err := rows.Scan(&hr.UserID, &hr.Name, &hr.Email, &hr.AvatarURL, &hr.Role, &hr.Priority, &archivedAt); err != nil {
			continue
		}
		hr.Archived = archivedAt.Valid
		out = append(out, hr)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// SetEventTypeHosts handles PUT /v1/event-types/{slug}/hosts — replaces the
// entire host list. Body: {"hosts":[{"user_id","role","priority"}]}.
func (h *Handler) SetEventTypeHosts(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	etID := h.eventTypeIDForOwner(w, r, r.PathValue("slug"), user.ID)
	if etID == "" {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Hosts []struct {
			UserID   string `json:"user_id"`
			Role     string `json:"role"`
			Priority int    `json:"priority"`
		} `json:"hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.Hosts) == 0 {
		h.writeError(w, http.StatusBadRequest, "an event type needs at least one host")
		return
	}

	// Validate roles and that there's at least one host who can actually take a
	// booking (a required or rotation host), so the event type is never bookable
	// with nobody to attend.
	seen := map[string]bool{}
	hasAttendee := false
	for _, hh := range req.Hosts {
		if hh.UserID == "" {
			h.writeError(w, http.StatusBadRequest, "each host needs a user_id")
			return
		}
		if seen[hh.UserID] {
			h.writeError(w, http.StatusBadRequest, "a member can only appear once in the host list")
			return
		}
		seen[hh.UserID] = true
		switch hh.Role {
		case "required", "rotation":
			hasAttendee = true
		case "optional":
		default:
			h.writeError(w, http.StatusBadRequest, "host role must be 'required', 'rotation', or 'optional'")
			return
		}
	}
	if !hasAttendee {
		h.writeError(w, http.StatusBadRequest, "at least one host must be required or in the rotation")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "set hosts: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	// Every host must be an existing, active member.
	for _, hh := range req.Hosts {
		var archivedAt sql.NullString
		err := tx.QueryRowContext(r.Context(),
			`SELECT archived_at FROM users WHERE id = ?`, hh.UserID).Scan(&archivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			h.writeError(w, http.StatusBadRequest, "host not found: "+hh.UserID)
			return
		}
		if err != nil {
			h.logger.ErrorContext(r.Context(), "set hosts: validate user", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if archivedAt.Valid {
			h.writeError(w, http.StatusBadRequest, "cannot add an archived member as a host")
			return
		}
	}

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM event_type_hosts WHERE event_type_id = ?`, etID); err != nil {
		h.logger.ErrorContext(r.Context(), "set hosts: clear", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, hh := range req.Hosts {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
			VALUES (?, ?, ?, ?, ?)`,
			uid.New(), etID, hh.UserID, hh.Role, hh.Priority); err != nil {
			h.logger.ErrorContext(r.Context(), "set hosts: insert", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "set hosts: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.ListEventTypeHosts(w, r)
}
