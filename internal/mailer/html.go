package mailer

import (
	"bytes"
	"html/template"
)

// HTML email rendering. Each email type has an html/template that defines a
// "content" block; it's cloned onto a shared base providing the outer layout
// (brand header, body slot, footer) plus reusable partials. Styling is inline
// with fixed light-palette hex colours and a table-based layout — CSS variables,
// <style> blocks, and external CSS are unreliable across Gmail/Outlook/Apple Mail.
// The plain-text body (booking.go) stays the multipart/alternative fallback.

const htmlLayout = `{{define "layout"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#f4f4f5;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;"><tr><td align="center" style="padding:24px 12px;">
<table role="presentation" cellpadding="0" cellspacing="0" style="width:100%;max-width:480px;background:#ffffff;border:1px solid #e4e4e7;border-radius:8px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
{{if .LogoURL}}<tr><td style="padding:18px 24px;border-bottom:1px solid #e4e4e7;">
<img src="{{.LogoURL}}" alt="{{.Brand}}" height="30" style="height:30px;max-height:30px;max-width:100%;display:block;border:0;">
</td></tr>{{end}}
<tr><td style="padding:24px;color:#18181b;font-size:15px;line-height:1.6;">
{{template "content" .}}
</td></tr>
</table>
</td></tr></table>
</body></html>{{end}}

{{define "cardOpen"}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;border-radius:8px;margin-bottom:20px;"><tr><td style="padding:14px 18px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;color:#18181b;">{{end}}
{{define "cardClose"}}</table></td></tr></table>{{end}}

{{define "mngBtn"}}{{if .ManageURL}}<a href="{{.ManageURL}}" style="display:block;text-align:center;text-decoration:none;background:#18181b;color:#ffffff;font-size:14px;font-weight:500;padding:11px 16px;border-radius:8px;margin-bottom:10px;">Manage booking</a>{{end}}{{end}}

{{define "calBtns"}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>
<td width="50%" style="padding-right:5px;"><a href="{{.GoogleCalURL}}" style="display:block;text-align:center;text-decoration:none;border:1px solid #d4d4d8;color:#18181b;font-size:13px;font-weight:500;padding:9px 8px;border-radius:8px;">Google Calendar</a></td>
<td width="50%" style="padding-left:5px;"><a href="{{.OutlookCalURL}}" style="display:block;text-align:center;text-decoration:none;border:1px solid #d4d4d8;color:#18181b;font-size:13px;font-weight:500;padding:9px 8px;border-radius:8px;">Outlook</a></td>
</tr></table>{{end}}

{{define "ref"}}<p style="margin:20px 0 0;font-size:12px;color:#a1a1aa;">Booking reference: {{.BookingID}}</p>{{end}}

{{define "note"}}{{if .CustomNote}}<div style="margin:16px 0 0;padding-top:16px;border-top:1px solid #e4e4e7;color:#52525b;font-size:13px;white-space:pre-line;">{{.CustomNote}}</div>{{end}}{{end}}

{{define "rebook"}}<a href="{{.BaseURL}}/book/{{.EventTypeSlug}}" style="display:block;text-align:center;text-decoration:none;background:#18181b;color:#ffffff;font-size:14px;font-weight:500;padding:11px 16px;border-radius:8px;">Book again</a>{{end}}`

// row markup shared by the content templates (label + value cell).
const labelTD = `<td style="color:#71717a;padding:5px 0;width:84px;vertical-align:top;">`
const valueTD = `<td style="padding:5px 0;vertical-align:top;">`

var htmlBase = template.Must(template.New("base").Parse(htmlLayout))

func content(def string) *template.Template {
	return template.Must(template.Must(htmlBase.Clone()).Parse(def))
}

// renderHTML executes the layout of a content template against d. On error it
// returns "" so the caller falls back to a text-only message.
func renderHTML(t *template.Template, d BookingData) string {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", d); err != nil {
		return ""
	}
	return buf.String()
}

var htmlConfirmOrg = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.OrganizerName}},</p>
<p style="margin:0 0 20px;color:#52525b;">Your booking is confirmed.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.HostName}}</td></tr>
<tr>` + labelTD + `When</td>` + valueTD + `{{.WhenFmt}}</td></tr>
{{if .LocationValue}}<tr>` + labelTD + `Location</td><td style="padding:5px 0;vertical-align:top;word-break:break-word;">{{.LocationValue}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "mngBtn" .}}
{{template "calBtns" .}}
{{template "ref" .}}
{{template "note" .}}
{{end}}`)

var htmlConfirmHost = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.HostName}},</p>
<p style="margin:0 0 20px;color:#52525b;">You have a new booking.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.OrganizerName}} &lt;{{.OrganizerEmail}}&gt;</td></tr>
<tr>` + labelTD + `When</td>` + valueTD + `{{.WhenFmt}}</td></tr>
{{if .LocationValue}}<tr>` + labelTD + `Location</td><td style="padding:5px 0;vertical-align:top;word-break:break-word;">{{.LocationValue}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "ref" .}}
{{end}}`)

var htmlCancelOrg = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.OrganizerName}},</p>
<p style="margin:0 0 20px;color:#52525b;">Your booking has been cancelled.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.HostName}}</td></tr>
<tr>` + labelTD + `When</td>` + valueTD + `{{.WhenFmt}}</td></tr>
{{if .CancellationReason}}<tr>` + labelTD + `Reason</td>` + valueTD + `{{.CancellationReason}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "rebook" .}}
{{template "note" .}}
{{end}}`)

var htmlCancelHost = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.HostName}},</p>
<p style="margin:0 0 20px;color:#52525b;">A booking has been cancelled.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.OrganizerName}} &lt;{{.OrganizerEmail}}&gt;</td></tr>
<tr>` + labelTD + `When</td>` + valueTD + `{{.WhenFmt}}</td></tr>
{{if .CancellationReason}}<tr>` + labelTD + `Reason</td>` + valueTD + `{{.CancellationReason}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "ref" .}}
{{end}}`)

var htmlRescheduleOrg = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.OrganizerName}},</p>
<p style="margin:0 0 20px;color:#52525b;">Your booking has been rescheduled.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.HostName}}</td></tr>
<tr><td style="color:#a1a1aa;padding:5px 0;width:84px;vertical-align:top;text-decoration:line-through;">Was</td><td style="padding:5px 0;vertical-align:top;color:#a1a1aa;text-decoration:line-through;">{{.PreviousStartFmt}}</td></tr>
<tr>` + labelTD + `Now</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.WhenFmt}}</td></tr>
{{if .LocationValue}}<tr>` + labelTD + `Location</td><td style="padding:5px 0;vertical-align:top;word-break:break-word;">{{.LocationValue}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "mngBtn" .}}
{{template "calBtns" .}}
{{template "ref" .}}
{{template "note" .}}
{{end}}`)

var htmlRescheduleHost = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.HostName}},</p>
<p style="margin:0 0 20px;color:#52525b;">A booking has been rescheduled.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.OrganizerName}} &lt;{{.OrganizerEmail}}&gt;</td></tr>
<tr><td style="color:#a1a1aa;padding:5px 0;width:84px;vertical-align:top;text-decoration:line-through;">Was</td><td style="padding:5px 0;vertical-align:top;color:#a1a1aa;text-decoration:line-through;">{{.PreviousStartFmt}}</td></tr>
<tr>` + labelTD + `Now</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.WhenFmt}}</td></tr>
{{if .LocationValue}}<tr>` + labelTD + `Location</td><td style="padding:5px 0;vertical-align:top;word-break:break-word;">{{.LocationValue}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "ref" .}}
{{end}}`)

var htmlReminderOrg = content(`{{define "content"}}
<p style="margin:0 0 4px;">Hi {{.OrganizerName}},</p>
<p style="margin:0 0 20px;color:#52525b;">This is a reminder that your booking is coming up.</p>
{{template "cardOpen" .}}
<tr>` + labelTD + `Event</td><td style="padding:5px 0;font-weight:500;vertical-align:top;">{{.EventTypeName}}</td></tr>
<tr>` + labelTD + `With</td>` + valueTD + `{{.HostName}}</td></tr>
<tr>` + labelTD + `When</td>` + valueTD + `{{.WhenFmt}}</td></tr>
{{if .LocationValue}}<tr>` + labelTD + `Location</td><td style="padding:5px 0;vertical-align:top;word-break:break-word;">{{.LocationValue}}</td></tr>{{end}}
{{template "cardClose" .}}
{{template "mngBtn" .}}
{{template "calBtns" .}}
{{template "ref" .}}
{{template "note" .}}
{{end}}`)

// htmlByType maps RenderBody email types to their HTML template (attendee-facing).
func htmlByType(emailType string) *template.Template {
	switch emailType {
	case "confirmation":
		return htmlConfirmOrg
	case "cancellation":
		return htmlCancelOrg
	case "reschedule":
		return htmlRescheduleOrg
	case "reminder":
		return htmlReminderOrg
	}
	return nil
}
