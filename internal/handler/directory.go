package handler

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/i18n"
)

//go:embed templates/directory.html
var directoryTmplSrc string

var directoryTmpl = template.Must(template.Must(template.New("directory").Funcs(template.FuncMap{
	"supportedLocales": i18n.SupportedLocales,
}).Parse(sharedPartialsSrc)).Parse(directoryTmplSrc))

type directoryItem struct {
	Slug string
	Name string
	Meta string // server-composed "duration · location · price" fragments
}

type directoryMember struct {
	Name   string
	Handle string // empty when the member has no public page
}

type directoryPageData struct {
	Locale     string
	Title      string
	Subtitle   string // server-composed sentence naming the person/team
	AvatarURL  string
	Initial    string
	Members    []directoryMember // team page only
	Items      []directoryItem
	CSSVersion string
	T          func(string) string

	HeadHTML         template.HTML
	GTMContainerID   string
	GA4MeasurementID string
	BusinessName     string
	LogoURL          string
	PrivacyURL       string
	TermsURL         string
	DemoMode         bool
}

// directoryItems lists public, active event types from a WHERE fragment, composing the
// rich meta line (duration, location, price) server-side so no date/plural logic is
// reinvented in the template.
func (h *Handler) directoryItems(ctx context.Context, loc *i18n.Locale, where string, args ...any) ([]directoryItem, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT slug, name, duration_minutes, location_type, COALESCE(location_value,''),
		       price_cents, currency
		FROM event_types et WHERE `+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []directoryItem{}
	for rows.Next() {
		var slug, name, locType, locValue, currency string
		var durMins, priceCents int
		if err := rows.Scan(&slug, &name, &durMins, &locType, &locValue, &priceCents, &currency); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, err
		}
		parts := []string{durationLabel(durMins, loc)}
		if ll := locationLabel(locType, locValue, loc); ll != "" {
			parts = append(parts, ll)
		}
		if pl := formatPrice(priceCents, currency); pl != "" {
			parts = append(parts, pl)
		}
		items = append(items, directoryItem{Slug: slug, Name: name, Meta: strings.Join(parts, " · ")})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (h *Handler) renderDirectory(w http.ResponseWriter, r *http.Request, data directoryPageData) {
	track := h.loadTrackingSettings(r.Context())
	brand := h.loadBranding(r.Context())
	loc := h.resolveLocaleWithFallback(r, brand.FallbackLocale)
	data.Locale = loc.Code
	data.T = loc.T
	data.CSSVersion = bookingCSSVersion
	data.HeadHTML = template.HTML(track.HeadHTML) // #nosec G203 -- admin-only code injection, intentionally raw, gated by requireAdmin
	data.GTMContainerID = track.GTMContainerID
	data.GA4MeasurementID = track.GA4MeasurementID
	data.BusinessName = brand.BusinessName
	data.LogoURL = brand.LogoURL
	data.PrivacyURL = brand.PrivacyURL
	data.TermsURL = brand.TermsURL
	data.DemoMode = h.demoMode
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := directoryTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "directory: render", "error", err)
	}
}

// PersonPage handles GET /u/{handle} — one person's public booking page: every public,
// active event type they own or host, with a per-type link into /book/{slug}. Archived
// users and unknown handles 404; users without a handle have no page. The /u/ prefix
// means handles need no reserved-word list (no top-level route can collide).
func (h *Handler) PersonPage(w http.ResponseWriter, r *http.Request) {
	handle := slugify(r.PathValue("handle"))
	if handle == "" {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	var userID, name, avatarURL string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT id, name, COALESCE(avatar_url,'') FROM users
		WHERE handle = ? AND archived_at IS NULL`, handle).
		Scan(&userID, &name, &avatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "person page: load user", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	brand := h.loadBranding(r.Context())
	loc := h.resolveLocaleWithFallback(r, brand.FallbackLocale)
	items, err := h.directoryItems(r.Context(), loc,
		`et.is_public = 1 AND et.is_active = 1 AND (et.user_id = ? OR EXISTS
		  (SELECT 1 FROM event_type_hosts hh WHERE hh.event_type_id = et.id AND hh.user_id = ?))`,
		userID, userID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "person page: load event types", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	h.renderDirectory(w, r, directoryPageData{
		Title:     name,
		Subtitle:  loc.Tf("directory_person_subtitle", name),
		AvatarURL: avatarURL,
		Initial:   firstRune(name),
		Items:     items,
	})
}

// TeamPage handles GET /team/{slug} — a team's public booking page: its public, active
// event types plus the member roster (each linking to /u/{handle} when set).
func (h *Handler) TeamPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var teamID, teamName string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, name FROM teams WHERE slug = ?`, slug).Scan(&teamID, &teamName)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team page: load team", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	mRows, err := h.db.QueryContext(r.Context(), `
		SELECT u.name, COALESCE(u.handle,'') FROM team_members tm
		JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = ? AND u.archived_at IS NULL
		ORDER BY u.name`, teamID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team page: load members", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	members := []directoryMember{}
	for mRows.Next() {
		var m directoryMember
		if err := mRows.Scan(&m.Name, &m.Handle); err != nil {
			mRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			h.logger.ErrorContext(r.Context(), "team page: scan member", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		members = append(members, m)
	}
	mRows.Close()
	if err := mRows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "team page: members rows", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	brand := h.loadBranding(r.Context())
	loc := h.resolveLocaleWithFallback(r, brand.FallbackLocale)
	items, err := h.directoryItems(r.Context(), loc,
		`et.team_id = ? AND et.is_public = 1 AND et.is_active = 1`, teamID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "team page: load event types", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	h.renderDirectory(w, r, directoryPageData{
		Title:    teamName,
		Subtitle: loc.Tf("directory_team_subtitle", teamName),
		Initial:  firstRune(teamName),
		Members:  members,
		Items:    items,
	})
}
