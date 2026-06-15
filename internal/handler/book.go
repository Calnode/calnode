package handler

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed templates/book.html
var bookTmplSrc string

var bookTmpl = template.Must(template.New("book").Parse(bookTmplSrc))

type bookQuestion struct {
	ID       string
	Label    string
	QType    string
	Options  []string
	Required bool
}

type bookPageData struct {
	Slug          string
	Name          string
	Description   template.HTML
	DurationLabel string
	HostName      string
	HostInitial   string
	AvatarURL     string
	LocationLabel string
	MaxFutureDays int
	Questions     []bookQuestion
}

func durationLabel(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	h := minutes / 60
	m := minutes % 60
	if m == 0 {
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	return fmt.Sprintf("%d hr %d min", h, m)
}

var mdRenderer = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

func renderMarkdown(src string) template.HTML {
	if src == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}

func locationLabel(locType, locValue string) string {
	switch locType {
	case "zoom":
		return "Zoom"
	case "google_meet":
		return "Google Meet"
	case "teams":
		return "Microsoft Teams"
	case "phone":
		return "Phone Call"
	case "in_person":
		if locValue != "" {
			return locValue
		}
		return "In Person"
	case "custom_video":
		return "Video Call"
	default:
		return "Video Call"
	}
}

// BookPage renders the public booking page for a given event type slug.
func (h *Handler) BookPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var (
		etID        string
		name        string
		description string
		durMins     int
		locType     string
		locValue    string
		maxDays     int
		hostName    string
		avatarURL   string
	)
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.id, et.name, COALESCE(et.description, ''),
		       et.duration_minutes, et.location_type, COALESCE(et.location_value, ''),
		       et.max_future_days, u.name, COALESCE(u.avatar_url, '')
		FROM event_types et
		JOIN users u ON u.id = et.user_id
		WHERE et.slug = ? AND et.is_active = 1 AND et.is_public = 1`,
		slug).Scan(&etID, &name, &description, &durMins, &locType, &locValue, &maxDays, &hostName, &avatarURL)

	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "book page: db query", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Load active intake questions.
	var questions []bookQuestion
	qRows, qErr := h.db.QueryContext(r.Context(), `
		SELECT id, label, type, COALESCE(options, '[]'), required
		FROM event_type_questions
		WHERE event_type_id = ?
		ORDER BY position`, etID)
	if qErr == nil {
		defer qRows.Close()
		for qRows.Next() {
			var q bookQuestion
			var optJSON string
			var req int
			if err := qRows.Scan(&q.ID, &q.Label, &q.QType, &optJSON, &req); err != nil {
				h.logger.ErrorContext(r.Context(), "book page: scan question", "error", err)
				continue
			}
			q.Required = req != 0
			if err := json.Unmarshal([]byte(optJSON), &q.Options); err != nil || q.Options == nil {
				q.Options = []string{}
			}
			questions = append(questions, q)
		}
		if err := qRows.Err(); err != nil {
			h.logger.ErrorContext(r.Context(), "book page: questions query", "error", err)
		}
	}

	initial := ""
	if len([]rune(hostName)) > 0 {
		initial = string([]rune(hostName)[0])
	}
	data := bookPageData{
		Slug:          slug,
		Name:          name,
		Description:   renderMarkdown(description),
		DurationLabel: durationLabel(durMins),
		HostName:      hostName,
		HostInitial:   initial,
		AvatarURL:     avatarURL,
		LocationLabel: locationLabel(locType, locValue),
		MaxFutureDays: maxDays,
		Questions:     questions,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := bookTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "book page: template", "error", err)
	}
}
