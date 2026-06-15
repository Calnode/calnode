package mailer

import (
	"bytes"
	"context"
	"fmt"
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
}

// StartFmt returns StartAt formatted in the organizer's timezone.
func (d BookingData) StartFmt() string { return inTZ(d.StartAt, d.OrganizerTimezone) }

// EndFmt returns EndAt formatted in the organizer's timezone.
func (d BookingData) EndFmt() string { return inTZ(d.EndAt, d.OrganizerTimezone) }

// PreviousStartFmt returns PreviousStartAt formatted in the organizer's timezone.
func (d BookingData) PreviousStartFmt() string { return inTZ(d.PreviousStartAt, d.OrganizerTimezone) }

// PreviousEndFmt returns PreviousEndAt formatted in the organizer's timezone.
func (d BookingData) PreviousEndFmt() string { return inTZ(d.PreviousEndAt, d.OrganizerTimezone) }

func inTZ(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon 2 Jan 2006, 3:04 PM MST")
}

// SendConfirmationToAttendee sends a booking confirmation email to the organizer/attendee.
func SendConfirmationToAttendee(ctx context.Context, m Mailer, d BookingData) error {
	if err := m.Send(ctx, Message{
		To:      []string{d.OrganizerEmail},
		Subject: "Booking confirmed: " + d.EventTypeName,
		Text:    render(confirmOrgTmpl, d),
	}); err != nil {
		return fmt.Errorf("mailer: confirmation (attendee): %w", err)
	}
	return nil
}

// SendConfirmationToHost sends a new-booking notification email to the host.
func SendConfirmationToHost(ctx context.Context, m Mailer, d BookingData) error {
	if d.HostEmail == "" {
		return nil
	}
	if err := m.Send(ctx, Message{
		To:      []string{d.HostEmail},
		Subject: "New booking: " + d.EventTypeName + " with " + d.OrganizerName,
		Text:    render(confirmHostTmpl, d),
	}); err != nil {
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
	if err := m.Send(ctx, Message{
		To:      []string{d.OrganizerEmail},
		Subject: "Booking cancelled: " + d.EventTypeName,
		Text:    render(cancelOrgTmpl, d),
	}); err != nil {
		return fmt.Errorf("mailer: cancellation (attendee): %w", err)
	}
	return nil
}

// SendCancellationToHost sends a cancellation notification to the host.
func SendCancellationToHost(ctx context.Context, m Mailer, d BookingData) error {
	if d.HostEmail == "" {
		return nil
	}
	if err := m.Send(ctx, Message{
		To:      []string{d.HostEmail},
		Subject: "Booking cancelled: " + d.EventTypeName + " with " + d.OrganizerName,
		Text:    render(cancelHostTmpl, d),
	}); err != nil {
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
	if err := m.Send(ctx, Message{
		To:      []string{d.OrganizerEmail},
		Subject: "Booking rescheduled: " + d.EventTypeName,
		Text:    render(rescheduleOrgTmpl, d),
	}); err != nil {
		return fmt.Errorf("mailer: reschedule (attendee): %w", err)
	}
	return nil
}

// SendRescheduleToHost sends a reschedule notification to the host.
func SendRescheduleToHost(ctx context.Context, m Mailer, d BookingData) error {
	if d.HostEmail == "" {
		return nil
	}
	if err := m.Send(ctx, Message{
		To:      []string{d.HostEmail},
		Subject: "Booking rescheduled: " + d.EventTypeName + " with " + d.OrganizerName,
		Text:    render(rescheduleHostTmpl, d),
	}); err != nil {
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
		Subject: "Reminder: " + d.EventTypeName + " is coming up",
		Text:    render(reminderOrgTmpl, d),
	}); err != nil {
		return fmt.Errorf("mailer: reminder: %w", err)
	}
	return nil
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
