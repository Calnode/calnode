package mailer

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"text/template"
	"time"

	"github.com/calnode/calnode/internal/i18n"
)

// BookingData carries all the information needed to render booking emails.
type BookingData struct {
	BookingID          string
	EventTypeName      string
	EventTypeSlug      string
	HostName           string
	HostEmail          string
	OrganizerName      string
	OrganizerEmail     string
	OrganizerTimezone  string
	StartAt            time.Time // UTC (new time for reschedule emails)
	EndAt              time.Time
	PreviousStartAt    time.Time // non-zero only for reschedule emails
	PreviousEndAt      time.Time
	LocationValue      string
	CancellationReason string
	ManageURL          string // manage link (reschedule/cancel), set at booking creation
	BaseURL            string
	CustomNote         string // optional host-configured note appended to the email body
	SubjectOverride    string // optional per-event-type custom subject; falls back to the default when empty
	// AttachICS attaches an iCalendar invite to the attendee's email — set by the
	// handler only when the host has no Google destination calendar (so Google
	// isn't already inviting the attendee, which would duplicate). ICSSequence must
	// be non-decreasing across a booking's confirm→reschedule→cancel lifecycle.
	AttachICS   bool
	ICSSequence int
	// Branding — instance-wide, threaded in by the handler. BrandName is the
	// wordmark/footer name (falls back to "Calnode" when empty); LogoURL is an
	// optional absolute https image shown in the HTML email header.
	BrandName     string
	LogoURL       string
	LogoHeight    int // email logo height in px; falls back to 28 (LogoPx)
	LogoOpacity   int // 20–100; CSS opacity for a subtle logo. 0/unset = 100 (opaque)
	BannerURL     string
	BannerOpacity int // 20–100; CSS opacity for the banner. 0/unset = 100 (opaque)
	// HideManageLink suppresses the "reschedule or cancel" footer link in HTML
	// emails. Set for host notifications — the manage token is the attendee's
	// self-serve link, not something the host should action from email.
	HideManageLink bool
	// Locale drives T/Tf and all date/time formatting below — the attendee's stored
	// booking_attendees.locale at send time, resolved by the caller. nil (the zero
	// value) falls back to English via locale(). Host-facing sends (SendConfirmationToHost
	// etc.) explicitly clear this to keep host emails in English regardless of the
	// attendee's language — see those functions' doc comments. Translation is
	// attendee-facing only, same "public-facing" scope as book.html/manage.html/embed.js;
	// host is the operator, out of scope (see internal-docs/i18n-plan.md).
	Locale *i18n.Locale
}

// locale returns d.Locale, or English if unset.
func (d BookingData) locale() *i18n.Locale {
	if d.Locale != nil {
		return d.Locale
	}
	return i18n.Default()
}

// T looks up a translation key in this booking's resolved locale.
func (d BookingData) T(key string) string { return d.locale().T(key) }

// LocaleCode returns the resolved locale's code (e.g. "es"), for the HTML email's
// <html lang="…"> attribute.
func (d BookingData) LocaleCode() string { return d.locale().Code }

// Tf is T with fmt.Sprintf-style argument substitution, for keys like "Hi %s,".
func (d BookingData) Tf(key string, args ...any) string {
	return fmt.Sprintf(d.locale().T(key), args...)
}

// Brand is the display name for the email wordmark/footer.
func (d BookingData) Brand() string {
	if d.BrandName != "" {
		return d.BrandName
	}
	return "Calnode"
}

// LogoPx is the email logo height in px, defaulting to 28 when unset.
func (d BookingData) LogoPx() int {
	if d.LogoHeight > 0 {
		return d.LogoHeight
	}
	return 28
}

// LogoOpacityCSS returns the logo opacity as a CSS value ("1", "0.6", …),
// defaulting to fully opaque when unset.
func (d BookingData) LogoOpacityCSS() string {
	o := d.LogoOpacity
	if o <= 0 || o > 100 {
		o = 100
	}
	return strconv.FormatFloat(float64(o)/100, 'f', -1, 64)
}

// BannerOpacityCSS returns the banner opacity as a CSS value ("1", "0.6", …),
// defaulting to fully opaque when unset.
func (d BookingData) BannerOpacityCSS() string {
	o := d.BannerOpacity
	if o <= 0 || o > 100 {
		o = 100
	}
	return strconv.FormatFloat(float64(o)/100, 'f', -1, 64)
}

// WhenFmt renders the booking time as a single human line in the organizer's timezone and
// resolved locale, e.g. "Mon 22 Jun 2026, 9:00 AM – 9:20 AM NZST" (English) or
// "lun 22 jun 2026, 21:00 – 21:20 NZST" (Spanish, 24h clock).
func (d BookingData) WhenFmt() string {
	tzLoc, err := time.LoadLocation(d.OrganizerTimezone)
	if err != nil {
		tzLoc = time.UTC
	}
	l := d.locale()
	start, end := d.StartAt.In(tzLoc), d.EndAt.In(tzLoc)
	return l.FormatDateTime(start) + " – " + l.FormatTimeOfDay(end) + " " + end.Format("MST")
}

// StartFmt returns StartAt formatted in the organizer's timezone and resolved locale.
func (d BookingData) StartFmt() string { return inTZ(d.StartAt, d.OrganizerTimezone, d.locale()) }

// EndFmt returns EndAt formatted in the organizer's timezone and resolved locale.
func (d BookingData) EndFmt() string { return inTZ(d.EndAt, d.OrganizerTimezone, d.locale()) }

// PreviousStartFmt returns PreviousStartAt formatted in the organizer's timezone and locale.
func (d BookingData) PreviousStartFmt() string {
	return inTZ(d.PreviousStartAt, d.OrganizerTimezone, d.locale())
}

// PreviousEndFmt returns PreviousEndAt formatted in the organizer's timezone and locale.
func (d BookingData) PreviousEndFmt() string {
	return inTZ(d.PreviousEndAt, d.OrganizerTimezone, d.locale())
}

// subjectOr returns the custom subject override when set, else the default.
func (d BookingData) subjectOr(def string) string {
	if d.SubjectOverride != "" {
		return d.SubjectOverride
	}
	return def
}

func inTZ(t time.Time, tz string, l *i18n.Locale) string {
	tzLoc, err := time.LoadLocation(tz)
	if err != nil {
		tzLoc = time.UTC
	}
	tt := t.In(tzLoc)
	return l.FormatDateTime(tt) + " " + tt.Format("MST")
}

// calDetails is the shared "add to calendar" description for the link builders.
func (d BookingData) calDetails() string {
	s := d.Tf("email_booking_with", d.HostName)
	if d.ManageURL != "" {
		s += "\n" + d.T("email_manage_this_booking") + " " + d.ManageURL
	}
	return s
}

// GoogleCalURL builds an "Add to Google Calendar" template link for the booking.
// It pre-fills a new event in the recipient's own calendar — pull-based, so it
// never duplicates a Google invite the host's calendar may have already sent.
func (d BookingData) GoogleCalURL() string {
	q := url.Values{}
	q.Set("action", "TEMPLATE")
	q.Set("text", d.EventTypeName)
	q.Set("dates", d.StartAt.UTC().Format("20060102T150405Z")+"/"+d.EndAt.UTC().Format("20060102T150405Z"))
	q.Set("details", d.calDetails())
	if d.LocationValue != "" {
		q.Set("location", d.LocationValue)
	}
	return "https://calendar.google.com/calendar/render?" + q.Encode()
}

// OutlookCalURL builds an "Add to Outlook (web) calendar" deep link for the booking.
func (d BookingData) OutlookCalURL() string {
	q := url.Values{}
	q.Set("path", "/calendar/action/compose")
	q.Set("rru", "addevent")
	q.Set("subject", d.EventTypeName)
	q.Set("startdt", d.StartAt.UTC().Format(time.RFC3339))
	q.Set("enddt", d.EndAt.UTC().Format(time.RFC3339))
	q.Set("body", d.calDetails())
	if d.LocationValue != "" {
		q.Set("location", d.LocationValue)
	}
	return "https://outlook.office.com/calendar/0/deeplink/compose?" + q.Encode()
}

// SendConfirmationToAttendee sends a booking confirmation email to the organizer/attendee.
func SendConfirmationToAttendee(ctx context.Context, m Mailer, d BookingData) error {
	msg := Message{
		To:      []string{d.OrganizerEmail},
		Subject: d.subjectOr(d.Tf("email_subject_confirmed", d.EventTypeName)),
		Text:    render(confirmOrgTmpl, d),
		HTML:    renderHTML(htmlConfirmOrg, d),
	}
	if d.AttachICS {
		msg.Attachments = []Attachment{icsAttachment(d, "REQUEST")}
	}
	if err := m.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer: confirmation (attendee): %w", err)
	}
	return nil
}

// SendConfirmationToHost sends a new-booking notification email to the host.
func SendConfirmationToHost(ctx context.Context, m Mailer, d BookingData) error {
	if d.HostEmail == "" {
		return nil
	}
	d.HideManageLink = true
	d.Locale = nil // host emails stay English regardless of the attendee's locale — see BookingData.Locale
	msg := Message{
		To:      []string{d.HostEmail},
		Subject: "New booking: " + d.EventTypeName + " with " + d.OrganizerName,
		Text:    render(confirmHostTmpl, d),
		HTML:    renderHTML(htmlConfirmHost, d),
	}
	if d.AttachICS {
		msg.Attachments = []Attachment{icsAttachment(d, "REQUEST")}
	}
	if err := m.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer: confirmation (host): %w", err)
	}
	return nil
}

// SendConfirmation sends booking confirmation emails to the organizer and the
// host. Kept as a convenience wrapper; callers that need fine-grained control
// should use SendConfirmationToAttendee / SendConfirmationToHost directly.
func SendConfirmation(ctx context.Context, m Mailer, d BookingData) error {
	var errs []string
	if err := SendConfirmationToAttendee(ctx, m, d); err != nil {
		errs = append(errs, err.Error())
	}
	if err := SendConfirmationToHost(ctx, m, d); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("mailer: confirmation: %v", errs)
	}
	return nil
}

// SendCancellationToAttendee sends a cancellation notification to the organizer/attendee.
func SendCancellationToAttendee(ctx context.Context, m Mailer, d BookingData) error {
	if d.OrganizerEmail == "" {
		return nil
	}
	msg := Message{
		To:      []string{d.OrganizerEmail},
		Subject: d.subjectOr(d.Tf("email_subject_cancelled", d.EventTypeName)),
		Text:    render(cancelOrgTmpl, d),
		HTML:    renderHTML(htmlCancelOrg, d),
	}
	if d.AttachICS {
		msg.Attachments = []Attachment{icsAttachment(d, "CANCEL")}
	}
	if err := m.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer: cancellation (attendee): %w", err)
	}
	return nil
}

// SendCancellationToHost sends a cancellation notification to the host.
func SendCancellationToHost(ctx context.Context, m Mailer, d BookingData) error {
	if d.HostEmail == "" {
		return nil
	}
	d.HideManageLink = true
	d.Locale = nil // host emails stay English regardless of the attendee's locale — see BookingData.Locale
	msg := Message{
		To:      []string{d.HostEmail},
		Subject: "Booking cancelled: " + d.EventTypeName + " with " + d.OrganizerName,
		Text:    render(cancelHostTmpl, d),
		HTML:    renderHTML(htmlCancelHost, d),
	}
	if d.AttachICS {
		msg.Attachments = []Attachment{icsAttachment(d, "CANCEL")}
	}
	if err := m.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer: cancellation (host): %w", err)
	}
	return nil
}

// SendCancellation sends cancellation emails to the organizer and the host.
// Kept as a convenience wrapper; callers that need fine-grained control
// should use SendCancellationToAttendee / SendCancellationToHost directly.
func SendCancellation(ctx context.Context, m Mailer, d BookingData) error {
	var errs []string
	if err := SendCancellationToAttendee(ctx, m, d); err != nil {
		errs = append(errs, err.Error())
	}
	if err := SendCancellationToHost(ctx, m, d); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("mailer: cancellation: %v", errs)
	}
	return nil
}

// SendRescheduleToAttendee sends a reschedule notification to the organizer/attendee.
// d.PreviousStartAt / PreviousEndAt must be set to the old times.
func SendRescheduleToAttendee(ctx context.Context, m Mailer, d BookingData) error {
	msg := Message{
		To:      []string{d.OrganizerEmail},
		Subject: d.subjectOr(d.Tf("email_subject_rescheduled", d.EventTypeName)),
		Text:    render(rescheduleOrgTmpl, d),
		HTML:    renderHTML(htmlRescheduleOrg, d),
	}
	if d.AttachICS {
		msg.Attachments = []Attachment{icsAttachment(d, "REQUEST")}
	}
	if err := m.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer: reschedule (attendee): %w", err)
	}
	return nil
}

// SendRescheduleToHost sends a reschedule notification to the host.
func SendRescheduleToHost(ctx context.Context, m Mailer, d BookingData) error {
	if d.HostEmail == "" {
		return nil
	}
	d.HideManageLink = true
	d.Locale = nil // host emails stay English regardless of the attendee's locale — see BookingData.Locale
	msg := Message{
		To:      []string{d.HostEmail},
		Subject: "Booking rescheduled: " + d.EventTypeName + " with " + d.OrganizerName,
		Text:    render(rescheduleHostTmpl, d),
		HTML:    renderHTML(htmlRescheduleHost, d),
	}
	if d.AttachICS {
		msg.Attachments = []Attachment{icsAttachment(d, "REQUEST")}
	}
	if err := m.Send(ctx, msg); err != nil {
		return fmt.Errorf("mailer: reschedule (host): %w", err)
	}
	return nil
}

// SendReschedule sends reschedule notification emails to the organizer and host.
// Kept as a convenience wrapper; callers that need fine-grained control
// should use SendRescheduleToAttendee / SendRescheduleToHost directly.
func SendReschedule(ctx context.Context, m Mailer, d BookingData) error {
	var errs []string
	if err := SendRescheduleToAttendee(ctx, m, d); err != nil {
		errs = append(errs, err.Error())
	}
	if err := SendRescheduleToHost(ctx, m, d); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("mailer: reschedule: %v", errs)
	}
	return nil
}

// SendReminder sends a reminder email to the organizer.
func SendReminder(ctx context.Context, m Mailer, d BookingData) error {
	if err := m.Send(ctx, Message{
		To:      []string{d.OrganizerEmail},
		Subject: d.subjectOr(d.Tf("email_subject_reminder", d.EventTypeName)),
		Text:    render(reminderOrgTmpl, d),
		HTML:    renderHTML(htmlReminderOrg, d),
	}); err != nil {
		return fmt.Errorf("mailer: reminder: %w", err)
	}
	return nil
}

// RenderBody renders the attendee-facing email for emailType and returns the
// subject, plain-text body, and HTML body. Returns ok=false for unrecognised
// emailType values. Valid types: "confirmation", "cancellation", "reschedule",
// "reminder".
func RenderBody(emailType string, d BookingData) (subject, body, html string, ok bool) {
	var def string
	var textTmpl *template.Template
	switch emailType {
	case "confirmation":
		def, textTmpl = d.Tf("email_subject_confirmed", d.EventTypeName), confirmOrgTmpl
	case "cancellation":
		def, textTmpl = d.Tf("email_subject_cancelled", d.EventTypeName), cancelOrgTmpl
	case "reschedule":
		def, textTmpl = d.Tf("email_subject_rescheduled", d.EventTypeName), rescheduleOrgTmpl
	case "reminder":
		def, textTmpl = d.Tf("email_subject_reminder", d.EventTypeName), reminderOrgTmpl
	default:
		return "", "", "", false
	}
	return d.subjectOr(def), render(textTmpl, d), renderHTML(htmlByType(emailType), d), true
}

func render(t *template.Template, d BookingData) string {
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return fmt.Sprintf("(template render error: %v)", err)
	}
	return buf.String()
}

var confirmOrgTmpl = template.Must(template.New("confirm-org").Parse(
	`{{.Tf "email_greeting" .OrganizerName}}

{{.T "email_confirmed_lead"}}

{{.T "email_label_event"}}:    {{.EventTypeName}}
{{.T "email_label_with"}}:     {{.HostName}}
{{.T "email_label_start"}}:    {{.StartFmt}}
{{.T "email_label_end"}}:      {{.EndFmt}}{{if .LocationValue}}
{{.T "email_label_location"}}: {{.LocationValue}}{{end}}

{{.T "email_add_to_calendar"}}
  {{.T "email_calendar_google"}}:  {{.GoogleCalURL}}
  {{.T "email_calendar_outlook"}}: {{.OutlookCalURL}}

{{.Tf "email_booking_reference" .BookingID}}
{{if .ManageURL}}
{{.T "email_manage_link_intro"}}
{{.ManageURL}}
{{else}}
{{.T "email_cancel_link_intro"}}
{{.BaseURL}}/book/{{.EventTypeSlug}}
{{end}}{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— {{.Brand}}
`))

var confirmHostTmpl = template.Must(template.New("confirm-host").Parse(
	`Hi {{.HostName}},

You have a new booking.

Event:    {{.EventTypeName}}
With:     {{.OrganizerName}} <{{.OrganizerEmail}}>
Start:    {{.StartFmt}}
End:      {{.EndFmt}}{{if .LocationValue}}
Location: {{.LocationValue}}{{end}}

Booking reference: {{.BookingID}}

— Calnode
`))

var cancelOrgTmpl = template.Must(template.New("cancel-org").Parse(
	`{{.Tf "email_greeting" .OrganizerName}}

{{.T "email_cancelled_lead"}}

{{.T "email_label_event"}}:  {{.EventTypeName}}
{{.T "email_label_with"}}:   {{.HostName}}
{{.T "email_label_start"}}:  {{.StartFmt}}
{{.T "email_label_end"}}:    {{.EndFmt}}{{if .CancellationReason}}
{{.T "email_label_reason"}}: {{.CancellationReason}}{{end}}

{{.T "email_rebook_intro"}}
{{.BaseURL}}/book/{{.EventTypeSlug}}
{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— {{.Brand}}
`))

var cancelHostTmpl = template.Must(template.New("cancel-host").Parse(
	`Hi {{.HostName}},

A booking has been cancelled.

Event:    {{.EventTypeName}}
With:     {{.OrganizerName}} <{{.OrganizerEmail}}>
Start:    {{.StartFmt}}
End:      {{.EndFmt}}{{if .CancellationReason}}
Reason:   {{.CancellationReason}}{{end}}

Booking reference: {{.BookingID}}

— Calnode
`))

var rescheduleOrgTmpl = template.Must(template.New("reschedule-org").Parse(
	`{{.Tf "email_greeting" .OrganizerName}}

{{.T "email_rescheduled_lead"}}

{{.T "email_label_event"}}:    {{.EventTypeName}}
{{.T "email_label_with"}}:     {{.HostName}}
{{.T "email_label_was"}}:      {{.PreviousStartFmt}}
{{.T "email_label_now"}}:      {{.StartFmt}}
{{.T "email_label_end"}}:      {{.EndFmt}}{{if .LocationValue}}
{{.T "email_label_location"}}: {{.LocationValue}}{{end}}

{{.T "email_add_to_calendar_updated"}}
  {{.T "email_calendar_google"}}:  {{.GoogleCalURL}}
  {{.T "email_calendar_outlook"}}: {{.OutlookCalURL}}

{{.Tf "email_booking_reference" .BookingID}}
{{if .ManageURL}}
{{.T "email_manage_link_intro_reschedule"}}
{{.ManageURL}}
{{end}}{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— {{.Brand}}
`))

var rescheduleHostTmpl = template.Must(template.New("reschedule-host").Parse(
	`Hi {{.HostName}},

A booking has been rescheduled.

Event:    {{.EventTypeName}}
With:     {{.OrganizerName}} <{{.OrganizerEmail}}>
Was:      {{.PreviousStartFmt}}
Now:      {{.StartFmt}}
End:      {{.EndFmt}}{{if .LocationValue}}
Location: {{.LocationValue}}{{end}}

Booking reference: {{.BookingID}}

— Calnode
`))

var reminderOrgTmpl = template.Must(template.New("reminder-org").Parse(
	`{{.Tf "email_greeting" .OrganizerName}}

{{.T "email_reminder_lead"}}

{{.T "email_label_event"}}:    {{.EventTypeName}}
{{.T "email_label_with"}}:     {{.HostName}}
{{.T "email_label_start"}}:    {{.StartFmt}}
{{.T "email_label_end"}}:      {{.EndFmt}}{{if .LocationValue}}
{{.T "email_label_location"}}: {{.LocationValue}}{{end}}

{{.T "email_add_to_calendar"}}
  {{.T "email_calendar_google"}}:  {{.GoogleCalURL}}
  {{.T "email_calendar_outlook"}}: {{.OutlookCalURL}}

{{.Tf "email_booking_reference" .BookingID}}
{{if .ManageURL}}
{{.T "email_manage_link_intro"}}
{{.ManageURL}}
{{end}}{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— {{.Brand}}
`))
