package handler

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

// Credential-bearing pages use only same-origin images and bundled scripts.
// Tracking, arbitrary head injection, assistants and Markdown embeds are omitted.
func credentialImage(value string) string {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\r\n") {
		return value
	}
	return ""
}

const credentialCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

var invitationStateTmpl = template.Must(template.New("invitation-state").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>Scheduling invitation</title><link rel="stylesheet" href="/booking.css"></head><body><main class="card"><h1>Scheduling invitation</h1><p>{{.}}</p></main></body></html>`))

func (h *Handler) InvitationPage(w http.ResponseWriter, r *http.Request) {
	invitationHeaders(w)
	w.Header().Set("Content-Security-Policy", credentialCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	inv, err := h.invitationByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		w.WriteHeader(404)
		_ = invitationStateTmpl.Execute(w, "Invalid invitation link. Contact the sender for a new link.")
		return
	}
	schedule, err := h.invitationSchedulingContext(r.Context(), inv)
	if err != nil {
		w.WriteHeader(410)
		_ = invitationStateTmpl.Execute(w, invitationState(inv))
		return
	}
	var name, slug, accent string
	if err := h.db.QueryRowContext(r.Context(), `SELECT et.name,et.slug,u.booking_accent FROM event_types et JOIN users u ON u.id=et.user_id WHERE et.id=?`, inv.EventTypeID).Scan(&name, &slug, &accent); err != nil {
		http.Error(w, "Invitation unavailable", 503)
		return
	}
	questions := []bookQuestion{}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,label,type,COALESCE(options,'[]'),required FROM event_type_questions WHERE event_type_id=? ORDER BY position`, inv.EventTypeID)
	if err != nil {
		http.Error(w, "Invitation unavailable", 503)
		return
	}
	for rows.Next() {
		var q bookQuestion
		var options string
		if err := rows.Scan(&q.ID, &q.Label, &q.QType, &options, &q.Required); err != nil {
			rows.Close()
			http.Error(w, "Invitation unavailable", 503)
			return
		}
		if err := json.Unmarshal([]byte(options), &q.Options); err != nil {
			rows.Close()
			http.Error(w, "Invitation unavailable", 503)
			return
		}
		questions = append(questions, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Invitation unavailable", 503)
		return
	}
	hosts := []hostDisplay{}
	for _, host := range schedule.Hosts {
		if host.Role == "optional" {
			continue
		}
		var name string
		if err := h.db.QueryRowContext(r.Context(), `SELECT name FROM users WHERE id=?`, host.UserID).Scan(&name); err != nil {
			http.Error(w, "Invitation unavailable", 503)
			return
		}
		hosts = append(hosts, hostDisplay{ID: host.UserID, Name: name, Initial: firstRune(name)})
	}
	if len(hosts) == 0 {
		w.WriteHeader(410)
		_ = invitationStateTmpl.Execute(w, invitationState(inv))
		return
	}
	for i := range hosts {
		hosts[i].Z = (len(hosts) - i) * 10
	}
	brand := h.loadBranding(r.Context())
	loc := h.resolveLocaleWithFallback(r, brand.FallbackLocale)
	translations, _ := loc.JSON()
	accent = accentOrDefault(accent)
	data := bookPageData{IsInvitation: true, RecipientName: inv.Recipient.Name, RecipientEmail: inv.Recipient.Email, SlotsURL: "/v1/schedule/" + r.PathValue("token") + "/slots", BookingURL: "/v1/schedule/" + r.PathValue("token") + "/book", Slug: slug, Name: name, DurationLabel: durationLabel(inv.DurationMinutes, loc), AccentColor: accent, AccentForeground: accentForeground(accent), Hosts: hosts, HostsLabel: hostsLabel(hosts, loc), HostName: hosts[0].Name, HostInitial: hosts[0].Initial, SoleHostName: soleHostName(hosts), LocationLabel: locationLabel(inv.LocationType, inv.LocationValue, loc), MinNoticeLabel: noticeLabel(schedule.Event.MinNoticeMinutes, loc), MaxFutureDays: schedule.Event.MaxFutureDays, Questions: questions, CSSVersion: bookingCSSVersion, BookingLogicJS: template.JS(bookingLogicJS), Locale: loc.Code, T: loc.T, I18NJSON: template.JS(translations), DataLayerFields: template.JS("[]"), QuestionsJSON: template.JS("{}"), BusinessName: brand.BusinessName, LogoURL: credentialImage(brand.LogoURL), LogoHeight: pageLogoHeight(brand.LogoHeight), LogoOpacity: opacityCSS(brand.LogoOpacity), BannerURL: credentialImage(brand.BannerURL), BannerOpacity: opacityCSS(brand.BannerOpacity), PrivacyURL: brand.PrivacyURL, TermsURL: brand.TermsURL}
	if data.RecipientName == "" {
		data.RecipientName = data.RecipientEmail
	}
	h.persistLangOverride(w, r)
	if err := bookTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "invitation page template failed", "error", err)
	}
}
