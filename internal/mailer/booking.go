package mailer

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"text/template"
	"time"
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
	BrandName  string
	LogoURL    string
	LogoHeight int // email logo height in px; falls back to 28 (LogoPx)
	// HideManageLink suppresses the "reschedule or cancel" footer link in HTML
	// emails. Set for host notifications — the manage token is the attendee's
	// self-serve link, not something the host should action from email.
	HideManageLink bool
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

// WhenFmt renders the booking time as a single human line in the organizer's
// timezone, e.g. "Mon 22 Jun 2026, 9:00 AM – 9:20 AM NZST".
func (d BookingData) WhenFmt() string {
	loc, err := time.LoadLocation(d.OrganizerTimezone)
	if err != nil {
		loc = time.UTC
	}
	return d.StartAt.In(loc).Format("Mon 2 Jan 2006, 3:04 PM") + " – " + d.EndAt.In(loc).Format("3:04 PM MST")
}

// StartFmt returns StartAt formatted in the organizer's timezone.
func (d BookingData) StartFmt() string { return inTZ(d.StartAt, d.OrganizerTimezone) }

// EndFmt returns EndAt formatted in the organizer's timezone.
func (d BookingData) EndFmt() string { return inTZ(d.EndAt, d.OrganizerTimezone) }

// PreviousStartFmt returns PreviousStartAt formatted in the organizer's timezone.
func (d BookingData) PreviousStartFmt() string { return inTZ(d.PreviousStartAt, d.OrganizerTimezone) }

// PreviousEndFmt returns PreviousEndAt formatted in the organizer's timezone.
func (d BookingData) PreviousEndFmt() string { return inTZ(d.PreviousEndAt, d.OrganizerTimezone) }

// subjectOr returns the custom subject override when set, else the default.
func (d BookingData) subjectOr(def string) string {
	if d.SubjectOverride != "" {
		return d.SubjectOverride
	}
	return def
}

func inTZ(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon 2 Jan 2006, 3:04 PM MST")
}

// calDetails is the shared "add to calendar" description for the link builders.
func (d BookingData) calDetails() string {
	s := "Booking with " + d.HostName
	if d.ManageURL != "" {
		s += "\nManage this booking: " + d.ManageURL
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
		Subject: d.subjectOr("Booking confirmed: " + d.EventTypeName),
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
		Subject: d.subjectOr("Booking cancelled: " + d.EventTypeName),
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
		Subject: d.subjectOr("Booking rescheduled: " + d.EventTypeName),
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
		Subject: d.subjectOr("Reminder: " + d.EventTypeName + " is coming up"),
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
		def, textTmpl = "Booking confirmed: "+d.EventTypeName, confirmOrgTmpl
	case "cancellation":
		def, textTmpl = "Booking cancelled: "+d.EventTypeName, cancelOrgTmpl
	case "reschedule":
		def, textTmpl = "Booking rescheduled: "+d.EventTypeName, rescheduleOrgTmpl
	case "reminder":
		def, textTmpl = "Reminder: "+d.EventTypeName+" is coming up", reminderOrgTmpl
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
	`Hi {{.OrganizerName}},

Your booking has been confirmed.

Event:    {{.EventTypeName}}
With:     {{.HostName}}
Start:    {{.StartFmt}}
End:      {{.EndFmt}}{{if .LocationValue}}
Location: {{.LocationValue}}{{end}}

Add to your calendar:
  Google:  {{.GoogleCalURL}}
  Outlook: {{.OutlookCalURL}}

Booking reference: {{.BookingID}}
{{if .ManageURL}}
To reschedule or cancel, visit:
{{.ManageURL}}
{{else}}
To cancel, visit:
{{.BaseURL}}/book/{{.EventTypeSlug}}
{{end}}{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— Calnode
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
	`Hi {{.OrganizerName}},

Your booking has been cancelled.

Event:    {{.EventTypeName}}
With:     {{.HostName}}
Start:    {{.StartFmt}}
End:      {{.EndFmt}}{{if .CancellationReason}}
Reason:   {{.CancellationReason}}{{end}}

To rebook, visit:
{{.BaseURL}}/book/{{.EventTypeSlug}}
{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— Calnode
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
	`Hi {{.OrganizerName}},

Your booking has been rescheduled.

Event:    {{.EventTypeName}}
With:     {{.HostName}}
Was:      {{.PreviousStartFmt}}
Now:      {{.StartFmt}}
End:      {{.EndFmt}}{{if .LocationValue}}
Location: {{.LocationValue}}{{end}}

Add to your calendar (updated time):
  Google:  {{.GoogleCalURL}}
  Outlook: {{.OutlookCalURL}}

Booking reference: {{.BookingID}}
{{if .ManageURL}}
To reschedule or cancel again, visit:
{{.ManageURL}}
{{end}}{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— Calnode
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
	`Hi {{.OrganizerName}},

This is a reminder that your booking is coming up.

Event:    {{.EventTypeName}}
With:     {{.HostName}}
Start:    {{.StartFmt}}
End:      {{.EndFmt}}{{if .LocationValue}}
Location: {{.LocationValue}}{{end}}

Add to your calendar:
  Google:  {{.GoogleCalURL}}
  Outlook: {{.OutlookCalURL}}

Booking reference: {{.BookingID}}
{{if .ManageURL}}
To reschedule or cancel, visit:
{{.ManageURL}}
{{end}}{{if .CustomNote}}
---
{{.CustomNote}}
{{end}}
— Calnode
`))
