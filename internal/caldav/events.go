package caldav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/uid"
)

// CreateEvent writes a new event to the user's CalDAV destination calendar via PUT of an
// iCalendar object. CalDAV has no native online-meeting link, so AddMeet is ignored and the
// returned joinURL is always empty. The returned eventID is the absolute resource URL, which
// UpdateEvent/CancelEvent use directly. Returns ("","",nil) if the user has no destination.
func (c *Client) CreateEvent(ctx context.Context, userID string, p calendar.CreateEventParams) (string, string, error) {
	cn, ok, err := c.loadConn(ctx, userID, -1, 1)
	if err != nil || !ok {
		return "", "", err
	}
	id := uid.New()
	resourceURL := joinURL(cn.calURL, id+".ics")
	ics := buildICS(id, p.Start, p.End, p.Summary, p.Description, p.Location, p.OrganizerName, p.OrganizerEmail, 0)

	status, _, err := c.putICS(ctx, resourceURL, cn.username, cn.password, ics, "*", "")
	if err != nil {
		return "", "", err
	}
	if status != http.StatusCreated && status != http.StatusNoContent && status != http.StatusOK {
		return "", "", fmt.Errorf("caldav: create event returned status %d", status)
	}
	return resourceURL, "", nil
}

// UpdateEvent moves an existing event to new start/end times. CalDAV has no partial update, so
// it GETs the current object, rewrites DTSTART/DTEND (and bumps SEQUENCE/DTSTAMP), and PUTs it
// back — preserving summary, description, location, and attendees.
func (c *Client) UpdateEvent(ctx context.Context, userID, eventID string, start, end time.Time) error {
	cn, ok, err := c.loadConn(ctx, userID, -1, 1)
	if err != nil || !ok {
		return err
	}
	body, etag, status, err := c.getICS(ctx, eventID, cn.username, cn.password)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil // already gone — nothing to move
	}
	if status != http.StatusOK {
		return fmt.Errorf("caldav: fetch event for update returned status %d", status)
	}
	updated := rewriteEventTimes(body, start, end)
	putStatus, _, err := c.putICS(ctx, eventID, cn.username, cn.password, updated, "", etag)
	if err != nil {
		return err
	}
	if putStatus != http.StatusCreated && putStatus != http.StatusNoContent && putStatus != http.StatusOK {
		return fmt.Errorf("caldav: update event returned status %d", putStatus)
	}
	return nil
}

// CancelEvent deletes the event resource from the destination calendar.
func (c *Client) CancelEvent(ctx context.Context, userID, eventID string) error {
	cn, ok, err := c.loadConn(ctx, userID, -1, 1)
	if err != nil || !ok {
		return err
	}
	status, _, _, err := c.do(ctx, http.MethodDelete, eventID, cn.username, cn.password, "", "")
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("caldav: delete event returned status %d", status)
	}
	return nil
}

// getICS fetches a calendar resource, returning its body, ETag, and HTTP status.
func (c *Client) getICS(ctx context.Context, rawURL, username, password string) (body, etag string, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", 0, err
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "text/calendar")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", "", 0, err
	}
	return string(b), resp.Header.Get("ETag"), resp.StatusCode, nil
}

// putICS PUTs an iCalendar object. ifNoneMatch="*" makes it create-only (fails if the
// resource exists); ifMatch sets the If-Match precondition for a safe update.
func (c *Client) putICS(ctx context.Context, rawURL, username, password, ics, ifNoneMatch, ifMatch string) (status int, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, strings.NewReader(ics))
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

// joinURL joins a collection URL and a resource name with exactly one slash.
func joinURL(base, name string) string {
	return strings.TrimRight(base, "/") + "/" + name
}

// buildICS renders a VCALENDAR/VEVENT for a booking. Times are emitted as UTC. Text values are
// escaped and long lines folded per RFC 5545.
func buildICS(id string, start, end time.Time, summary, description, location, orgName, orgEmail string, sequence int) string {
	var b strings.Builder
	w := func(line string) { b.WriteString(foldLine(line)); b.WriteString("\r\n") }
	w("BEGIN:VCALENDAR")
	w("VERSION:2.0")
	w("PRODID:-//Calnode//Booking//EN")
	w("CALSCALE:GREGORIAN")
	w("METHOD:REQUEST")
	w("BEGIN:VEVENT")
	w("UID:" + id + "@calnode")
	w("DTSTAMP:" + icsUTC(time.Now()))
	w("DTSTART:" + icsUTC(start))
	w("DTEND:" + icsUTC(end))
	w("SUMMARY:" + escapeText(summary))
	if description != "" {
		w("DESCRIPTION:" + escapeText(description))
	}
	if location != "" {
		w("LOCATION:" + escapeText(location))
	}
	if orgEmail != "" {
		org := "ORGANIZER"
		if orgName != "" {
			org += ";CN=" + escapeText(orgName)
		}
		w(org + ":mailto:" + orgEmail)
	}
	w(fmt.Sprintf("SEQUENCE:%d", sequence))
	w("STATUS:CONFIRMED")
	w("END:VEVENT")
	w("END:VCALENDAR")
	return b.String()
}

// rewriteEventTimes replaces the DTSTART/DTEND lines of an existing iCalendar object with new
// UTC times, drops any DURATION (now redundant), refreshes DTSTAMP/LAST-MODIFIED, and bumps
// SEQUENCE — preserving every other line. Operates on raw (unfolded-safe) lines so it never
// disturbs the rest of the object.
func rewriteEventTimes(ics string, start, end time.Time) string {
	lines := strings.Split(strings.ReplaceAll(ics, "\r\n", "\n"), "\n")
	var out []string
	sawSeq := false
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		upper := strings.ToUpper(ln)
		head := propName(upper)
		switch head {
		case "DTSTART":
			out = append(out, "DTSTART:"+icsUTC(start))
		case "DTEND":
			out = append(out, "DTEND:"+icsUTC(end))
		case "DURATION":
			// drop — DTEND is now authoritative
		case "DTSTAMP", "LAST-MODIFIED":
			out = append(out, head+":"+icsUTC(time.Now()))
		case "SEQUENCE":
			sawSeq = true
			out = append(out, fmt.Sprintf("SEQUENCE:%d", seqPlusOne(ln)))
		case "END":
			if strings.EqualFold(strings.TrimSpace(propValue(ln)), "VEVENT") && !sawSeq {
				out = append(out, "SEQUENCE:1")
				sawSeq = true
			}
			out = append(out, ln)
		default:
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\r\n") + "\r\n"
}

func propName(line string) string {
	end := len(line)
	for i, r := range line {
		if r == ':' || r == ';' {
			end = i
			break
		}
	}
	return line[:end]
}

func propValue(line string) string {
	if i := strings.IndexByte(line, ':'); i >= 0 {
		return line[i+1:]
	}
	return ""
}

func seqPlusOne(line string) int {
	return atoi(strings.TrimSpace(propValue(line))) + 1
}

// escapeText escapes a value per RFC 5545 (backslash, semicolon, comma, newline).
func escapeText(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		";", `\;`,
		",", `\,`,
		"\r\n", `\n`,
		"\n", `\n`,
		"\r", `\n`,
	)
	return r.Replace(s)
}

// foldLine folds a content line to <=75 octets per RFC 5545, continuation lines beginning with
// a single space. Folds on byte boundaries (ASCII-safe for our generated content).
func foldLine(line string) string {
	const limit = 73 // leave room; continuation adds a leading space
	if len(line) <= 75 {
		return line
	}
	var b strings.Builder
	b.WriteString(line[:limit])
	rest := line[limit:]
	for len(rest) > 0 {
		b.WriteString("\r\n ")
		n := limit
		if len(rest) < n {
			n = len(rest)
		}
		b.WriteString(rest[:n])
		rest = rest[n:]
	}
	return b.String()
}
