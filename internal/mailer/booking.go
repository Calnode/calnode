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
	StartAt            time.Time // UTC
	EndAt              time.Time // UTC
	LocationValue      string
	CancellationReason string
	BaseURL            string
}

// StartFmt returns StartAt formatted in the organizer's timezone.
func (d BookingData) StartFmt() string { return inTZ(d.StartAt, d.OrganizerTimezone) }

// EndFmt returns EndAt formatted in the organizer's timezone.
func (d BookingData) EndFmt() string { return inTZ(d.EndAt, d.OrganizerTimezone) }

func inTZ(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon 2 Jan 2006, 3:04 PM MST")
}

// SendConfirmation sends booking confirmation emails to the organizer and the
// host. If either send fails the error is returned but the other send is still
// attempted. The caller should log the error and continue — a failed email must
// not undo a successful booking.
func SendConfirmation(ctx context.Context, m Mailer, d BookingData) error {
	var errs []string

	if err := m.Send(ctx, Message{
		To:      []string{d.OrganizerEmail},
		Subject: "Booking confirmed: " + d.EventTypeName,
		Text:    render(confirmOrgTmpl, d),
	}); err != nil {
		errs = append(errs, "organizer: "+err.Error())
	}

	if d.HostEmail != "" {
		if err := m.Send(ctx, Message{
			To:      []string{d.HostEmail},
			Subject: "New booking: " + d.EventTypeName + " with " + d.OrganizerName,
			Text:    render(confirmHostTmpl, d),
		}); err != nil {
			errs = append(errs, "host: "+err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("mailer: confirmation: %v", errs)
	}
	return nil
}

// SendCancellation sends cancellation emails to the organizer and the host.
func SendCancellation(ctx context.Context, m Mailer, d BookingData) error {
	var errs []string

	if d.OrganizerEmail != "" {
		if err := m.Send(ctx, Message{
			To:      []string{d.OrganizerEmail},
			Subject: "Booking cancelled: " + d.EventTypeName,
			Text:    render(cancelOrgTmpl, d),
		}); err != nil {
			errs = append(errs, "organizer: "+err.Error())
		}
	}

	if d.HostEmail != "" {
		if err := m.Send(ctx, Message{
			To:      []string{d.HostEmail},
			Subject: "Booking cancelled: " + d.EventTypeName + " with " + d.OrganizerName,
			Text:    render(cancelHostTmpl, d),
		}); err != nil {
			errs = append(errs, "host: "+err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("mailer: cancellation: %v", errs)
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

To cancel, visit:
{{.BaseURL}}/bookings/{{.BookingID}}

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
