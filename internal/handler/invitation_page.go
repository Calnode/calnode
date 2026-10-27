package handler

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/i18n"
)

// Credential-bearing pages use only same-origin images and bundled scripts.
// Tracking, arbitrary head injection, assistants and Markdown embeds are omitted.
func credentialImage(value string) string {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\r\n") {
		return value
	}
	return ""
}

// #nosec G101 -- Content Security Policy directives, not a credential or secret.
const credentialCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

var invitationStateTmpl = template.Must(template.New("invitation-state").Parse(`<!doctype html><html lang="{{.Locale}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>{{.Title}}</title><link rel="stylesheet" href="/booking.css"></head><body><main class="card"><h1>{{.Title}}</h1><p>{{.Message}}</p></main></body></html>`))

func (h *Handler) InvitationPage(w http.ResponseWriter, r *http.Request) {
	brand := h.loadBranding(r.Context())
	loc := h.resolveLocaleWithFallback(r, brand.FallbackLocale)
	h.persistLangOverride(w, r)
	w.Header().Set("Vary", "Accept-Language, Cookie")
	invitationHeaders(w)
	w.Header().Set("Content-Security-Policy", credentialCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	inv, err := h.invitationByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.renderInvitationState(w, r, loc, http.StatusNotFound, loc.T("invitation_invalid"))
		return
	}
	schedule, err := h.invitationSchedulingContext(r.Context(), inv)
	if err != nil {
		h.renderInvitationState(w, r, loc, http.StatusGone, invitationState(inv, loc))
		return
	}
	var name, slug, accent string
	if err := h.db.QueryRowContext(r.Context(), `SELECT et.name,et.slug,u.booking_accent FROM event_types et JOIN users u ON u.id=et.user_id WHERE et.id=?`, inv.EventTypeID).Scan(&name, &slug, &accent); err != nil {
		h.renderInvitationState(w, r, loc, http.StatusServiceUnavailable, loc.T("err_invitation_unavailable"))
		return
	}
	questions := []bookQuestion{}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,label,type,COALESCE(options,'[]'),required FROM event_type_questions WHERE event_type_id=? ORDER BY position`, inv.EventTypeID)
	if err != nil {
		h.renderInvitationState(w, r, loc, http.StatusServiceUnavailable, loc.T("err_invitation_unavailable"))
		return
	}
	for rows.Next() {
		var q bookQuestion
		var options string
		if err := rows.Scan(&q.ID, &q.Label, &q.QType, &options, &q.Required); err != nil {
			rows.Close() // #nosec G104 -- cleanup after scan failure; preserve the original error
			h.renderInvitationState(w, r, loc, http.StatusServiceUnavailable, loc.T("err_invitation_unavailable"))
			return
		}
		if err := json.Unmarshal([]byte(options), &q.Options); err != nil {
			rows.Close() // #nosec G104 -- cleanup after JSON decode failure; preserve the original error
			h.renderInvitationState(w, r, loc, http.StatusServiceUnavailable, loc.T("err_invitation_unavailable"))
			return
		}
		questions = append(questions, q)
	}
	err = rows.Err()
	rows.Close() // #nosec G104 -- rows fully consumed; iteration/driver errors checked through rows.Err()
	if err != nil {
		h.renderInvitationState(w, r, loc, http.StatusServiceUnavailable, loc.T("err_invitation_unavailable"))
		return
	}
	hosts := []hostDisplay{}
	for _, host := range schedule.Hosts {
		if host.Role == "optional" {
			continue
		}
		var name string
		if err := h.db.QueryRowContext(r.Context(), `SELECT name FROM users WHERE id=?`, host.UserID).Scan(&name); err != nil {
			h.renderInvitationState(w, r, loc, http.StatusServiceUnavailable, loc.T("err_invitation_unavailable"))
			return
		}
		hosts = append(hosts, hostDisplay{ID: host.UserID, Name: name, Initial: firstRune(name)})
	}
	if len(hosts) == 0 {
		h.renderInvitationState(w, r, loc, http.StatusGone, invitationState(inv, loc))
		return
	}
	for i := range hosts {
		hosts[i].Z = (len(hosts) - i) * 10
	}
	translations, _ := loc.JSON()
	accent = accentOrDefault(accent)
	data := bookPageData{
		IsInvitation:     true,
		RecipientName:    inv.Recipient.Name,
		RecipientEmail:   inv.Recipient.Email,
		SlotsURL:         "/v1/schedule/" + r.PathValue("token") + "/slots",
		BookingURL:       "/v1/schedule/" + r.PathValue("token") + "/book",
		Slug:             slug,
		Name:             name,
		DurationLabel:    durationLabel(inv.DurationMinutes, loc),
		AccentColor:      accent,
		AccentForeground: accentForeground(accent),
		Hosts:            hosts,
		HostsLabel:       hostsLabel(hosts, loc),
		HostName:         hosts[0].Name,
		HostInitial:      hosts[0].Initial,
		SoleHostName:     soleHostName(hosts),
		LocationLabel:    locationLabel(inv.LocationType, inv.LocationValue, loc),
		MinNoticeLabel:   noticeLabel(schedule.Event.MinNoticeMinutes, loc),
		MaxFutureDays:    schedule.Event.MaxFutureDays,
		Questions:        questions,
		CSSVersion:       bookingCSSVersion,
		BookingLogicJS:   template.JS(bookingLogicJS), // #nosec G203 -- embedded assets/booking-logic.js, not user input
		Locale:           loc.Code,
		T:                loc.T,
		I18NJSON:         template.JS(translations), // #nosec G203 -- Locale.JSON uses json.Marshal, which escapes <, > and & for script embedding
		DataLayerFields:  template.JS("[]"),
		QuestionsJSON:    template.JS("{}"),
		BusinessName:     brand.BusinessName,
		LogoURL:          credentialImage(brand.LogoURL),
		LogoHeight:       pageLogoHeight(brand.LogoHeight),
		LogoOpacity:      opacityCSS(brand.LogoOpacity),
		BannerURL:        credentialImage(brand.BannerURL),
		BannerOpacity:    opacityCSS(brand.BannerOpacity),
		PrivacyURL:       brand.PrivacyURL,
		TermsURL:         brand.TermsURL,
	}
	if data.RecipientName == "" {
		data.RecipientName = data.RecipientEmail
	}
	if err := bookTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "invitation page template failed", "error", err)
	}
}

func (h *Handler) renderInvitationState(w http.ResponseWriter, r *http.Request, loc *i18n.Locale, status int, message string) {
	w.WriteHeader(status)
	data := struct {
		Locale, Title, Message string
	}{loc.Code, loc.T("invitation_title"), message}
	if err := invitationStateTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "invitation state template failed", "error", err)
	}
}
