package mailer

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
)

// ErrNoReply means a received email carries no iCalendar REPLY: an ordinary reply typed by
// a person, an auto-responder, or a forwarded invite. Not an error to retry.
var ErrNoReply = errors.New("itip: no calendar REPLY in message")

// ICSReply is the part of an iTIP REPLY (RFC 5546 §3.2.3) that says who answered what.
type ICSReply struct {
	UID       string
	Attendees []ICSReplyAttendee
}

// ICSReplyAttendee is one ATTENDEE line of a REPLY. PartStat is lower-cased
// ("accepted", "declined", "tentative", "needs-action", ...).
type ICSReplyAttendee struct {
	Email    string
	PartStat string
}

// maxICSPart bounds how much of one calendar part is read; a REPLY is a few hundred bytes.
const maxICSPart = 256 << 10

// ParseICSReply finds the METHOD:REPLY calendar object in a raw RFC 5322 message. Mail
// clients put it in a text/calendar body part (Gmail, Outlook, Apple Mail), often with a
// second copy as an .ics attachment; the first REPLY found wins.
func ParseICSReply(raw []byte) (ICSReply, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ICSReply{}, fmt.Errorf("itip: read message: %w", err)
	}
	var found ICSReply
	done, walkErr := walkParts(msg.Header, msg.Body, 0, func(body []byte) bool {
		r, ok := parseReplyCalendar(body)
		if ok {
			found = r
		}
		return ok
	})
	if done {
		return found, nil
	}
	if walkErr != nil {
		return ICSReply{}, walkErr
	}
	return ICSReply{}, ErrNoReply
}

type partHeader interface{ Get(string) string }

// walkParts visits every calendar-looking leaf part, depth-first, and stops as soon as
// visit reports it found what it wanted (done).
func walkParts(h partHeader, body io.Reader, depth int, visit func([]byte) bool) (done bool, err error) {
	if depth > 8 {
		return false, errors.New("itip: MIME nesting too deep")
	}
	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err == io.EOF {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("itip: read part: %w", err)
			}
			if done, err := walkParts(p.Header, p, depth+1, visit); done || err != nil {
				return done, err
			}
		}
	}
	if !isCalendarPart(mediaType, h) {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(decodeTransfer(h.Get("Content-Transfer-Encoding"), body), maxICSPart))
	if err != nil {
		return false, nil // a malformed part is skipped; another copy may still be readable
	}
	return visit(data), nil
}

func isCalendarPart(mediaType string, h partHeader) bool {
	if mediaType == "text/calendar" || mediaType == "application/ics" {
		return true
	}
	if _, params, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil {
		return strings.HasSuffix(strings.ToLower(params["filename"]), ".ics")
	}
	return false
}

// decodeTransfer undoes a part's Content-Transfer-Encoding. The base64 decoder already
// skips the CR/LF that MIME wraps bodies with.
func decodeTransfer(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

// parseReplyCalendar extracts the REPLY from one iCalendar object; ok is false when the
// object is not a REPLY (an echoed REQUEST, a CANCEL) or has no VEVENT UID.
func parseReplyCalendar(data []byte) (ICSReply, bool) {
	var (
		reply   ICSReply
		isReply bool
		inEvent bool
	)
	for _, line := range unfoldICS(data) {
		name, params, value := splitICSLine(line)
		switch {
		case name == "METHOD":
			isReply = strings.EqualFold(value, "REPLY")
		case name == "BEGIN" && strings.EqualFold(value, "VEVENT"):
			inEvent = true
		case name == "END" && strings.EqualFold(value, "VEVENT"):
			inEvent = false
		case inEvent && name == "UID" && reply.UID == "":
			reply.UID = value
		case inEvent && name == "ATTENDEE":
			addr := value
			if len(addr) > 7 && strings.EqualFold(addr[:7], "mailto:") {
				addr = addr[7:]
			}
			stat := strings.ToLower(params["PARTSTAT"])
			if stat == "" {
				stat = "needs-action"
			}
			reply.Attendees = append(reply.Attendees, ICSReplyAttendee{Email: strings.TrimSpace(addr), PartStat: stat})
		}
	}
	return reply, isReply && reply.UID != "" && len(reply.Attendees) > 0
}

// unfoldICS splits content lines and joins RFC 5545 §3.1 continuations (a line starting
// with a space or tab continues the previous one).
func unfoldICS(data []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxICSPart)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// splitICSLine splits `NAME;P1=V1;P2="V;2":value` into the upper-cased name, its
// parameters (names upper-cased, quotes removed) and the value. Colons and semicolons
// inside quoted parameter values do not split.
func splitICSLine(line string) (string, map[string]string, string) {
	inQuote := false
	colon := -1
	for i, c := range line {
		if c == '"' {
			inQuote = !inQuote
		} else if c == ':' && !inQuote {
			colon = i
			break
		}
	}
	if colon < 0 {
		return "", nil, ""
	}
	head, value := line[:colon], line[colon+1:]
	var segs []string
	inQuote = false
	start := 0
	for i, c := range head {
		if c == '"' {
			inQuote = !inQuote
		} else if c == ';' && !inQuote {
			segs = append(segs, head[start:i])
			start = i + 1
		}
	}
	segs = append(segs, head[start:])
	params := make(map[string]string, len(segs)-1)
	for _, s := range segs[1:] {
		if k, v, ok := strings.Cut(s, "="); ok {
			params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return strings.ToUpper(segs[0]), params, value
}
