package handler

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed templates/book.html
var bookTmplSrc string

var bookTmpl = template.Must(template.New("book").Parse(bookTmplSrc))

type bookPageData struct {
	Slug          string
	Name          string
	Description   string
	DurationLabel string
	HostName      string
	LocationLabel string
	MaxFutureDays int
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
		name        string
		description string
		durMins     int
		locType     string
		locValue    string
		maxDays     int
		hostName    string
	)
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.name, COALESCE(et.description, ''),
		       et.duration_minutes, et.location_type, COALESCE(et.location_value, ''),
		       et.max_future_days, u.name
		FROM event_types et
		JOIN users u ON u.id = et.user_id
		WHERE et.slug = ? AND et.is_active = 1 AND et.is_public = 1`,
		slug).Scan(&name, &description, &durMins, &locType, &locValue, &maxDays, &hostName)

	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "book page: db query", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := bookPageData{
		Slug:          slug,
		Name:          name,
		Description:   description,
		DurationLabel: durationLabel(durMins),
		HostName:      hostName,
		LocationLabel: locationLabel(locType, locValue),
		MaxFutureDays: maxDays,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := bookTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "book page: template", "error", err)
	}
}
