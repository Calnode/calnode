package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/uid"
)

// questionJSON is the API representation of an event-type intake question.
type questionJSON struct {
	ID            string   `json:"id"`
	EventTypeID   string   `json:"event_type_id"`
	Label         string   `json:"label"`
	Type          string   `json:"type"`
	Options       []string `json:"options,omitempty"`
	Required      bool     `json:"required"`
	Position      int      `json:"position"`
}

// answerJSON is the API representation of a booking answer.
type answerJSON struct {
	QuestionID string `json:"question_id"`
	Label      string `json:"label"`
	Type       string `json:"type"`
	Value      string `json:"value"`
}

// ListQuestions handles GET /v1/event-types/{slug}/questions (public).
// Returns questions ordered by position for the booking form.
// Only returns questions for active, public event types.
func (h *Handler) ListQuestions(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var etID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM event_types WHERE slug = ? AND is_active = 1 AND is_public = 1`, slug).Scan(&etID)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list questions: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, event_type_id, label, type, options, required, position
		FROM event_type_questions
		WHERE event_type_id = ?
		ORDER BY position, id`, etID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list questions: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []questionJSON{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list questions: scan", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, *q)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list questions: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ListQuestionsAdmin handles GET /v1/event-types/{slug}/questions/admin (auth required).
// Like ListQuestions but skips is_active/is_public checks so admins can manage
// questions on inactive or private event types.
func (h *Handler) ListQuestionsAdmin(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")

	var etID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM event_types WHERE slug = ? AND user_id = ?`, slug, user.ID).Scan(&etID)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list questions admin: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, event_type_id, label, type, options, required, position
		FROM event_type_questions
		WHERE event_type_id = ?
		ORDER BY position, id`, etID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list questions admin: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []questionJSON{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list questions admin: scan", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, *q)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list questions admin: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateQuestion handles POST /v1/event-types/{slug}/questions (admin).
func (h *Handler) CreateQuestion(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Label    string   `json:"label"`
		Type     string   `json:"type"`
		Options  []string `json:"options"`
		Required bool     `json:"required"`
		Position *int     `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.Type = strings.TrimSpace(req.Type)
	if req.Label == "" {
		h.writeError(w, http.StatusBadRequest, "label is required")
		return
	}
	switch req.Type {
	case "text", "checkbox":
	case "select":
		if len(req.Options) == 0 {
			h.writeError(w, http.StatusBadRequest, "options is required for type 'select'")
			return
		}
	default:
		h.writeError(w, http.StatusBadRequest, "type must be one of: text, select, checkbox")
		return
	}

	// Verify caller owns this event type.
	var etID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM event_types WHERE slug = ? AND user_id = ?`, slug, user.ID).Scan(&etID)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create question: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Serialise options for select type.
	var optJSON *string
	if req.Type == "select" {
		b, _ := json.Marshal(req.Options)
		s := string(b)
		optJSON = &s
	}

	id := uid.New()
	requiredInt := 0
	if req.Required {
		requiredInt = 1
	}

	var position int
	if req.Position != nil {
		position = *req.Position
		if _, err := h.db.ExecContext(r.Context(), `
			INSERT INTO event_type_questions (id, event_type_id, label, type, options, required, position)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, etID, req.Label, req.Type, optJSON, requiredInt, position); err != nil {
			h.logger.ErrorContext(r.Context(), "create question: insert", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		// Compute position atomically in the INSERT to avoid a SELECT+INSERT race
		// when two requests arrive concurrently for the same event type.
		row := h.db.QueryRowContext(r.Context(), `
			INSERT INTO event_type_questions (id, event_type_id, label, type, options, required, position)
			VALUES (?, ?, ?, ?, ?, ?,
				(SELECT COALESCE(MAX(position)+1, 0) FROM event_type_questions WHERE event_type_id = ?))
			RETURNING position`,
			id, etID, req.Label, req.Type, optJSON, requiredInt, etID)
		if err := row.Scan(&position); err != nil {
			h.logger.ErrorContext(r.Context(), "create question: insert", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	q := &questionJSON{
		ID:          id,
		EventTypeID: etID,
		Label:       req.Label,
		Type:        req.Type,
		Options:     req.Options,
		Required:    req.Required,
		Position:    position,
	}
	h.writeJSON(w, http.StatusCreated, q)
}

// UpdateQuestion handles PATCH /v1/event-types/{slug}/questions/{id} (admin).
func (h *Handler) UpdateQuestion(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")
	qID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Label    *string  `json:"label"`
		Type     *string  `json:"type"`
		Options  []string `json:"options"`
		Required *bool    `json:"required"`
		Position *int     `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Load current question verifying ownership via event_types.user_id.
	var current questionJSON
	var optRaw sql.NullString
	var requiredInt int
	err := h.db.QueryRowContext(r.Context(), `
		SELECT q.id, q.event_type_id, q.label, q.type, q.options, q.required, q.position
		FROM event_type_questions q
		JOIN event_types et ON et.id = q.event_type_id
		WHERE q.id = ? AND et.slug = ? AND et.user_id = ?`, qID, slug, user.ID).
		Scan(&current.ID, &current.EventTypeID, &current.Label, &current.Type,
			&optRaw, &requiredInt, &current.Position)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "question not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update question: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	current.Required = requiredInt != 0
	if optRaw.Valid {
		_ = json.Unmarshal([]byte(optRaw.String), &current.Options)
	}

	// Apply partial updates.
	if req.Label != nil {
		current.Label = strings.TrimSpace(*req.Label)
		if current.Label == "" {
			h.writeError(w, http.StatusBadRequest, "label must not be empty")
			return
		}
	}
	if req.Type != nil {
		switch *req.Type {
		case "text", "checkbox", "select":
			current.Type = *req.Type
		default:
			h.writeError(w, http.StatusBadRequest, "type must be one of: text, select, checkbox")
			return
		}
	}
	if req.Options != nil {
		current.Options = req.Options
	}
	if current.Type == "select" && len(current.Options) == 0 {
		h.writeError(w, http.StatusBadRequest, "options is required for type 'select'")
		return
	}
	if req.Required != nil {
		current.Required = *req.Required
	}
	if req.Position != nil {
		current.Position = *req.Position
	}

	var optJSON *string
	if current.Type == "select" {
		b, _ := json.Marshal(current.Options)
		s := string(b)
		optJSON = &s
	}
	requiredSave := 0
	if current.Required {
		requiredSave = 1
	}
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE event_type_questions
		SET label = ?, type = ?, options = ?, required = ?, position = ?
		WHERE id = ?`,
		current.Label, current.Type, optJSON, requiredSave, current.Position, qID); err != nil {
		h.logger.ErrorContext(r.Context(), "update question: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, current)
}

// DeleteQuestion handles DELETE /v1/event-types/{slug}/questions/{id} (admin).
func (h *Handler) DeleteQuestion(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")
	qID := r.PathValue("id")

	res, err := h.db.ExecContext(r.Context(), `
		DELETE FROM event_type_questions
		WHERE id = (
			SELECT q.id FROM event_type_questions q
			JOIN event_types et ON et.id = q.event_type_id
			WHERE q.id = ? AND et.slug = ? AND et.user_id = ?
		)`, qID, slug, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete question", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "question not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetBookingAnswers handles GET /v1/bookings/{id}/answers (admin).
// Returns the intake question answers for a booking owned by the caller.
func (h *Handler) GetBookingAnswers(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")

	// Verify caller owns the booking.
	var hostID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT host_id FROM bookings WHERE id = ?`, id).Scan(&hostID)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get booking answers: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if hostID != user.ID {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT a.question_id, q.label, q.type, a.value
		FROM booking_answers a
		JOIN event_type_questions q ON q.id = a.question_id
		WHERE a.booking_id = ?
		ORDER BY q.position, q.id`, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get booking answers: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []answerJSON{}
	for rows.Next() {
		var a answerJSON
		if err := rows.Scan(&a.QuestionID, &a.Label, &a.Type, &a.Value); err != nil {
			h.logger.ErrorContext(r.Context(), "get booking answers: scan", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "get booking answers: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// scanQuestion scans a row from event_type_questions into a questionJSON.
type questionScanner interface {
	Scan(dest ...any) error
}

func scanQuestion(s questionScanner) (*questionJSON, error) {
	var q questionJSON
	var optRaw sql.NullString
	var requiredInt int
	if err := s.Scan(&q.ID, &q.EventTypeID, &q.Label, &q.Type, &optRaw, &requiredInt, &q.Position); err != nil {
		return nil, err
	}
	q.Required = requiredInt != 0
	if optRaw.Valid && optRaw.String != "" {
		_ = json.Unmarshal([]byte(optRaw.String), &q.Options)
	}
	return &q, nil
}

// validateAnswers loads the questions for an event type, checks that all required
// questions have answers, validates select-option values, and rejects answers for
// questions that don't belong to this event type. Returns the []booking.Answer
// slice ready for bookingSvc.Create, or writes an error response and returns an error.
// answerError is a client-fault validation failure from validateAnswersCore (unknown
// or duplicate question, missing required field, invalid select option). The REST
// wrapper maps it to 400; other errors (DB failures) map to 500. The MCP tool surfaces
// its message directly.
type answerError struct{ msg string }

func (e *answerError) Error() string { return e.msg }

// validateAnswers is the HTTP wrapper: it runs validateAnswersCore and translates the
// result into a status code (400 for client-fault answer errors, 500 otherwise).
func (h *Handler) validateAnswers(w http.ResponseWriter, r *http.Request, eventTypeID string, rawAnswers []struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
}) ([]booking.Answer, error) {
	in := make([]booking.Answer, len(rawAnswers))
	for i, a := range rawAnswers {
		in[i] = booking.Answer{QuestionID: a.QuestionID, Value: a.Value}
	}
	out, err := h.validateAnswersCore(r.Context(), eventTypeID, in)
	if err != nil {
		var ae *answerError
		if errors.As(err, &ae) {
			h.writeError(w, http.StatusBadRequest, ae.msg)
		} else {
			h.logger.ErrorContext(r.Context(), "validate answers", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
		}
		return nil, err
	}
	return out, nil
}

// validateAnswersCore validates submitted intake answers against an event type's
// questions and returns the canonical answer slice. Shared by the REST booking handler
// and the MCP create_booking tool — no HTTP coupling. Client-fault problems are
// returned as *answerError; DB failures as plain wrapped errors.
func (h *Handler) validateAnswersCore(ctx context.Context, eventTypeID string, rawAnswers []booking.Answer) ([]booking.Answer, error) {
	// Load all questions for this event type (label included for error messages).
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, label, type, options, required
		FROM event_type_questions WHERE event_type_id = ?`, eventTypeID)
	if err != nil {
		return nil, fmt.Errorf("validate answers: load questions: %w", err)
	}
	defer rows.Close()

	type question struct {
		id       string
		label    string
		qtype    string
		options  []string
		required bool
	}
	questions := map[string]question{}
	for rows.Next() {
		var q question
		var optRaw sql.NullString
		var reqInt int
		if err := rows.Scan(&q.id, &q.label, &q.qtype, &optRaw, &reqInt); err != nil {
			return nil, fmt.Errorf("validate answers: scan: %w", err)
		}
		q.required = reqInt != 0
		if optRaw.Valid && optRaw.String != "" {
			_ = json.Unmarshal([]byte(optRaw.String), &q.options)
		}
		questions[q.id] = q
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("validate answers: rows: %w", err)
	}

	// Index submitted answers by question_id; reject duplicates and unknown IDs.
	submitted := map[string]string{}
	for _, a := range rawAnswers {
		if _, ok := questions[a.QuestionID]; !ok {
			return nil, &answerError{fmt.Sprintf("unknown question_id %q", a.QuestionID)}
		}
		if _, dup := submitted[a.QuestionID]; dup {
			return nil, &answerError{fmt.Sprintf("duplicate answer for question_id %q", a.QuestionID)}
		}
		submitted[a.QuestionID] = a.Value
	}

	// Validate each question.
	var out []booking.Answer
	for _, q := range questions {
		val, answered := submitted[q.id]
		if q.required && (!answered || strings.TrimSpace(val) == "") {
			return nil, &answerError{fmt.Sprintf("required field %q is missing", q.label)}
		}
		if q.qtype == "select" && answered && val != "" {
			valid := false
			for _, opt := range q.options {
				if opt == val {
					valid = true
					break
				}
			}
			if !valid {
				return nil, &answerError{fmt.Sprintf("invalid option for %q: %q is not an allowed choice", q.label, val)}
			}
		}
		if answered {
			out = append(out, booking.Answer{QuestionID: q.id, Value: val})
		}
	}
	return out, nil
}
