package mailer

import (
	"fmt"
	"strings"
	"time"
)

// BuildICS renders an RFC 5545 iCalendar for the booking using the given METHOD:
// "REQUEST" for a confirmation or reschedule, "CANCEL" to withdraw it. The UID is
// stable per booking so a calendar client matches the original REQUEST, its
// reschedule update, and the eventual CANCEL to the same event; d.ICSSequence must
// not decrease across that lifecycle (the handlers feed it updated_at's unix time).
//
// This is only used when the host has no Google destination calendar — otherwise
// Google already invites the attendee and a second invite would duplicate it.
func BuildICS(d BookingData, method string) []byte {
	status := "CONFIRMED"
	if method == "CANCEL" {
		status = "CANCELLED"
	}
	stamp := time.Now().UTC().Format(icsTimeLayout)

	var b strings.Builder
	writeICSLine(&b, "BEGIN:VCALENDAR")
	writeICSLine(&b, "VERSION:2.0")
	writeICSLine(&b, "PRODID:-//Calnode//Booking//EN")
	writeICSLine(&b, "CALSCALE:GREGORIAN")
	writeICSLine(&b, "METHOD:"+method)
	writeICSLine(&b, "BEGIN:VEVENT")
	writeICSLine(&b, "UID:"+d.BookingID+"@calnode")
	writeICSLine(&b, fmt.Sprintf("SEQUENCE:%d", d.ICSSequence))
	writeICSLine(&b, "DTSTAMP:"+stamp)
	writeICSLine(&b, "DTSTART:"+d.StartAt.UTC().Format(icsTimeLayout))
	writeICSLine(&b, "DTEND:"+d.EndAt.UTC().Format(icsTimeLayout))
	writeICSLine(&b, "SUMMARY:"+escapeICSText(d.EventTypeName))
	desc := "Booking with " + d.HostName
	if d.ManageURL != "" {
		desc += "\nManage this booking: " + d.ManageURL
	}
	writeICSLine(&b, "DESCRIPTION:"+escapeICSText(desc))
	if d.LocationValue != "" {
		writeICSLine(&b, "LOCATION:"+escapeICSText(d.LocationValue))
	}
	if d.HostEmail != "" {
		writeICSLine(&b, "ORGANIZER;CN="+escapeICSParam(d.HostName)+":mailto:"+d.HostEmail)
	}
	if d.OrganizerEmail != "" {
		writeICSLine(&b, "ATTENDEE;CN="+escapeICSParam(d.OrganizerName)+";RSVP=TRUE:mailto:"+d.OrganizerEmail)
	}
	writeICSLine(&b, "STATUS:"+status)
	writeICSLine(&b, "END:VEVENT")
	writeICSLine(&b, "END:VCALENDAR")
	return []byte(b.String())
}

const icsTimeLayout = "20060102T150405Z"

// icsAttachment wraps BuildICS as an email attachment with the right calendar
// MIME type (the method param is what makes clients render the invite UI).
func icsAttachment(d BookingData, method string) Attachment {
	return Attachment{
		Filename:    "invite.ics",
		ContentType: "text/calendar; charset=utf-8; method=" + method,
		Content:     BuildICS(d, method),
	}
}

// escapeICSText escapes a value for an iCalendar TEXT field (RFC 5545 §3.3.11).
func escapeICSText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// escapeICSParam quotes a property-parameter value (e.g. CN) so embedded spaces,
// commas, or colons can't break parsing. Double quotes aren't allowed inside a
// quoted param value, so they're stripped.
func escapeICSParam(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, "") + `"`
}

// writeICSLine appends a content line, folding it to ≤75 octets per RFC 5545 §3.1
// (continuation lines begin with a single space) and terminating with CRLF. Folds
// on octet boundaries; our values are URLs/ASCII names, so this won't split a
// multi-byte rune in practice.
func writeICSLine(b *strings.Builder, line string) {
	const max = 75
	if len(line) <= max {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}
	b.WriteString(line[:max])
	b.WriteString("\r\n")
	rest := line[max:]
	for len(rest) > 0 {
		n := max - 1 // leading space consumes one octet of the 75-octet budget
		if len(rest) < n {
			n = len(rest)
		}
		b.WriteString(" ")
		b.WriteString(rest[:n])
		b.WriteString("\r\n")
		rest = rest[n:]
	}
}
