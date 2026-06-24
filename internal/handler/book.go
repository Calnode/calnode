package handler

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

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
	Hosts         []hostDisplay // faces for the info panel (1 = single, >1 = group stack)
	HostsLabel    string        // "Alex, Sam & 2 others" for the group case
	LocationLabel string
	PriceLabel    string // formatted price (e.g. "$50.00"); empty for free events
	PriceCents    int    // raw price for the dataLayer conversion value (0 = free)
	Currency      string // ISO 4217, lowercase
	MaxFutureDays int
	Questions     []bookQuestion
	// AssistantEnabled shows the conversational-booking chat panel when the LLM layer is on.
	AssistantEnabled bool
	// CSSVersion cache-busts the /booking.css link (content hash).
	CSSVersion string
	// Tracking
	HeadHTML         template.HTML // operator-configured <head> code injection (trusted)
	GTMContainerID   string        // native GTM container (validated GTM-XXXX); "" = off
	GA4MeasurementID string        // native GA4 measurement id (validated G-XXXX); "" = off
	DataLayerEnabled bool
	DataLayerFields  template.JS // JSON array of enabled dataLayer field keys
	QuestionsJSON    template.JS // {questionID: label} map for labelling answers in dataLayer
	// Branding
	BusinessName string
	LogoURL      string
	LogoHeight   int
	LogoOpacity  string // CSS opacity value, e.g. "1" or "0.6"
}

// hostDisplay is one host's identity for the public booking page.
type hostDisplay struct {
	ID        string
	Name      string
	Initial   string
	AvatarURL string
	Z         int // stacking order: leftmost face paints on top of the next
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

// displayHosts resolves the faces to show for an event type's booking page by
// routing mode: the single required host (Normal), the top-priority rotation host
// (round-robin — best-effort, the actual pick happens at booking time), or all
// required hosts (Group). Archived members are excluded.
func (h *Handler) displayHosts(ctx context.Context, etID, mode string) []hostDisplay {
	role := "required"
	if mode == "round_robin" {
		role = "rotation"
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT u.name, COALESCE(u.avatar_url, '')
		FROM event_type_hosts eth JOIN users u ON u.id = eth.user_id
		WHERE eth.event_type_id = ? AND eth.role = ? AND u.archived_at IS NULL
		ORDER BY eth.priority ASC, u.name ASC`, etID, role)
	if err != nil {
		h.logger.ErrorContext(ctx, "book page: display hosts", "error", err)
		return nil
	}
	defer rows.Close()
	var out []hostDisplay
	for rows.Next() {
		var name, avatar string
		if err := rows.Scan(&name, &avatar); err != nil {
			continue
		}
		out = append(out, hostDisplay{Name: name, Initial: firstRune(name), AvatarURL: avatar})
	}
	// Round-robin shows a single default face (the top-priority host); the JS swaps
	// it per selected slot. Other modes show the whole required set.
	if mode == "round_robin" && len(out) > 1 {
		out = out[:1]
	}
	return out
}

// hostsLabel renders a group host list as "Alex, Sam & 2 others" (first names).
func hostsLabel(hosts []hostDisplay) string {
	first := func(name string) string {
		for i, r := range name {
			if r == ' ' {
				return name[:i]
			}
		}
		return name
	}
	n := len(hosts)
	switch n {
	case 0:
		return ""
	case 1:
		return hosts[0].Name
	case 2:
		return first(hosts[0].Name) + " & " + first(hosts[1].Name)
	case 3:
		return first(hosts[0].Name) + ", " + first(hosts[1].Name) + " & " + first(hosts[2].Name)
	default:
		unit := "others"
		if n-3 == 1 {
			unit = "other"
		}
		return fmt.Sprintf("%s, %s, %s & %d %s",
			first(hosts[0].Name), first(hosts[1].Name), first(hosts[2].Name), n-3, unit)
	}
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

// formatPrice renders an amount in minor units for display, using a symbol for common
// currencies and falling back to "AMOUNT CODE". Returns "" for free (cents <= 0).
func formatPrice(cents int, currency string) string {
	if cents <= 0 {
		return ""
	}
	amount := float64(cents) / 100
	switch strings.ToLower(currency) {
	case "usd":
		return fmt.Sprintf("$%.2f", amount)
	case "eur":
		return fmt.Sprintf("€%.2f", amount)
	case "gbp":
		return fmt.Sprintf("£%.2f", amount)
	case "aud":
		return fmt.Sprintf("A$%.2f", amount)
	case "cad":
		return fmt.Sprintf("C$%.2f", amount)
	case "nzd":
		return fmt.Sprintf("NZ$%.2f", amount)
	default:
		return fmt.Sprintf("%.2f %s", amount, strings.ToUpper(currency))
	}
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

// PublicEventType handles GET /v1/event-types/{slug}/public — the public display
// info a booking client (e.g. the embeddable widget) needs before rendering the
// form: name, duration, location label, host faces, and brand. No PII, no auth;
// only active + public event types. Slots, intake questions, and booking creation
// are separate public endpoints the client calls next.
func (h *Handler) PublicEventType(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var (
		etID, name, description, locType, locValue string
		hostName, avatarURL, routingMode, currency string
		durMins, maxDays, priceCents               int
	)
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.id, et.name, COALESCE(et.description, ''),
		       et.duration_minutes, et.location_type, COALESCE(et.location_value, ''),
		       et.max_future_days, et.routing_mode, u.name, COALESCE(u.avatar_url, ''),
		       et.price_cents, et.currency
		FROM event_types et
		JOIN users u ON u.id = et.user_id
		WHERE et.slug = ? AND et.is_active = 1 AND et.is_public = 1`,
		slug).Scan(&etID, &name, &description, &durMins, &locType, &locValue, &maxDays, &routingMode, &hostName, &avatarURL, &priceCents, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "public event type: db query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	hosts := h.displayHosts(r.Context(), etID, routingMode)
	if len(hosts) == 0 {
		hosts = []hostDisplay{{Name: hostName, AvatarURL: avatarURL}}
	}
	// Absolutise relative asset paths so they resolve from a remote embedding page.
	abs := func(p string) string {
		if p != "" && strings.HasPrefix(p, "/") {
			return h.publicURL() + p
		}
		return p
	}
	type pubHost struct {
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url,omitempty"`
	}
	outHosts := make([]pubHost, 0, len(hosts))
	for _, hd := range hosts {
		outHosts = append(outHosts, pubHost{Name: hd.Name, AvatarURL: abs(hd.AvatarURL)})
	}

	brand := h.loadBranding(r.Context())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"slug":             slug,
		"name":             name,
		"description":      description,
		"duration_minutes": durMins,
		"location_type":    locType,
		"location_label":   locationLabel(locType, locValue),
		"max_future_days":  maxDays,
		"assistant_enabled": h.getLLM() != nil,
		"price_cents":      priceCents,
		"currency":         currency,
		"hosts":            outHosts,
		"business_name":    brand.BusinessName,
		"logo_url":         abs(brand.LogoURL),
	})
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
		routingMode string
		priceCents  int
		currency    string
	)
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.id, et.name, COALESCE(et.description, ''),
		       et.duration_minutes, et.location_type, COALESCE(et.location_value, ''),
		       et.max_future_days, et.routing_mode, u.name, COALESCE(u.avatar_url, ''),
		       et.price_cents, et.currency
		FROM event_types et
		JOIN users u ON u.id = et.user_id
		WHERE et.slug = ? AND et.is_active = 1 AND et.is_public = 1`,
		slug).Scan(&etID, &name, &description, &durMins, &locType, &locValue, &maxDays, &routingMode, &hostName, &avatarURL, &priceCents, &currency)

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

	// Resolve the host face(s) by routing mode; fall back to the event-type owner
	// if no hosts are configured (shouldn't happen post-backfill).
	hosts := h.displayHosts(r.Context(), etID, routingMode)
	if len(hosts) == 0 {
		hosts = []hostDisplay{{Name: hostName, Initial: firstRune(hostName), AvatarURL: avatarURL}}
	}
	for i := range hosts {
		hosts[i].Z = (len(hosts) - i) * 10
	}
	track := h.loadTrackingSettings(r.Context())
	brand := h.loadBranding(r.Context())
	dlFields, _ := json.Marshal(track.DataLayerFields)
	qmap := make(map[string]string, len(questions))
	for _, q := range questions {
		qmap[q.ID] = q.Label
	}
	qjson, _ := json.Marshal(qmap)

	data := bookPageData{
		Slug:          slug,
		Name:          name,
		Description:   renderMarkdown(description),
		DurationLabel: durationLabel(durMins),
		HostName:      hosts[0].Name,
		HostInitial:   hosts[0].Initial,
		AvatarURL:     hosts[0].AvatarURL,
		Hosts:         hosts,
		HostsLabel:    hostsLabel(hosts),
		LocationLabel: locationLabel(locType, locValue),
		PriceLabel:    formatPrice(priceCents, currency),
		PriceCents:    priceCents,
		Currency:      currency,
		MaxFutureDays:    maxDays,
		Questions:        questions,
		AssistantEnabled: h.getLLM() != nil,
		CSSVersion:       bookingCSSVersion,

		HeadHTML:         template.HTML(track.HeadHTML),
		GTMContainerID:   track.GTMContainerID,
		GA4MeasurementID: track.GA4MeasurementID,
		DataLayerEnabled: track.DataLayerEnabled,
		DataLayerFields:  template.JS(dlFields),
		QuestionsJSON:    template.JS(qjson),

		BusinessName: brand.BusinessName,
		LogoURL:      brand.LogoURL,
		LogoHeight:   pageLogoHeight(brand.LogoHeight),
		LogoOpacity:  opacityCSS(brand.LogoOpacity),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", publicCSP(track))
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := bookTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "book page: template", "error", err)
	}
}
