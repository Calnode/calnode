package caldav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/uid"
)

// CreateEvent writes a new event to the user's CalDAV destination calendar via PUT of an
// iCalendar object. CalDAV has no native online-meeting link, so AddMeet is ignored and the
// returned joinURL is always empty. The returned eventID is the absolute resource URL, which
// UpdateEvent/CancelEvent use directly. Returns ("","",nil) if the user has no destination.
func (c *Client) CreateEvent(ctx context.Context, userID string, p calendar.CreateEventParams) (string, string, string, error) {
	cn, ok, err := c.loadConn(ctx, userID, -1, 1)
	if err != nil || !ok {
		return "", "", "", err
	}
	id := uid.New()
	resourceURL := joinURL(cn.calURL, id+".ics")
	ics := buildICS(id, p.Start, p.End, p.Summary, p.Description, p.Location, p.OrganizerName, p.OrganizerEmail, 0)

	status, _, err := c.putICS(ctx, resourceURL, cn.username, cn.password, ics, "*", "")
	if err != nil {
		return "", "", "", err
	}
	if status != http.StatusCreated && status != http.StatusNoContent && status != http.StatusOK {
		return "", "", "", fmt.Errorf("caldav: create event returned status %d", status)
	}
	// The event id is already the absolute resource URL, so it carries its own location and
	// Update/Cancel need nothing extra. Report the collection anyway for symmetry with the
	// other providers and so the stored value is meaningful if it is ever inspected.
	return resourceURL, "", cn.calURL, nil
}

// UpdateEvent moves an existing event to new start/end times. CalDAV has no partial update, so
// it GETs the current object, rewrites DTSTART/DTEND (and bumps SEQUENCE/DTSTAMP), and PUTs it
// back — preserving summary, description, location, and attendees.
//
// It authenticates as the account that holds the event (eventConn), not as the current
// destination, and returns an error without sending anything when that account cannot be
// established.
func (c *Client) UpdateEvent(ctx context.Context, userID, calendarID, eventID string, start, end time.Time) error {
	cn, ok, err := c.eventConn(ctx, userID, calendarID, eventID)
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

// CancelEvent deletes the event resource from the calendar it was written to, as the account
// that holds it (see UpdateEvent).
func (c *Client) CancelEvent(ctx context.Context, userID, calendarID, eventID string) error {
	cn, ok, err := c.eventConn(ctx, userID, calendarID, eventID)
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

// RecognizesEvent reports whether eventID is a CalDAV event id: the absolute http(s) URL of
// the event resource, which is what CreateEvent returns. Google and Microsoft ids are opaque
// tokens and never URLs, so calendar.Service uses this to send an update or cancel here even
// after the user's destination has moved to another provider.
func (c *Client) RecognizesEvent(eventID string) bool {
	_, ok := parseDAVURL(eventID)
	return ok
}

var (
	errEventNotURL    = errors.New("caldav: event id is not a CalDAV resource URL; nothing was sent")
	errNoEventOwner   = errors.New("caldav: none of the user's CalDAV accounts holds this event (it is not inside any of their calendars); nothing was sent")
	errManyEventOwner = errors.New("caldav: more than one of the user's CalDAV accounts could hold this event; nothing was sent")
)

// eventAccount is one connected CalDAV account and every collection URL it is known to use: the
// calendar bound at connect, then any calendars saved for the account in connection_calendars.
type eventAccount struct {
	id, username, pwEnc string
	collections         []string
}

// eventConn resolves the connection an update or cancel of eventID must authenticate as: the
// account that holds the event, never whichever account is the destination now. A host can
// connect several CalDAV accounts on different servers and move the destination between them,
// and an event's URL names the server it lives on, so using the destination's credentials sends
// that account's password to another account's server.
//
// ok=false with a nil error means the user has no CalDAV connection at all, so there is no
// account to act as (the Provider contract's "no matching connection"). Otherwise it returns an
// error, having sent nothing, unless pickEventOwner finds exactly one account.
func (c *Client) eventConn(ctx context.Context, userID, calendarID, eventID string) (conn, bool, error) {
	var accounts []eventAccount
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(account_email,''), access_token_enc, calendar_id
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'caldav'`, userID)
	if err != nil {
		return conn{}, false, fmt.Errorf("caldav: load connections: %w", err)
	}
	for rows.Next() {
		var a eventAccount
		var bound string
		if err := rows.Scan(&a.id, &a.username, &a.pwEnc, &bound); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return conn{}, false, fmt.Errorf("caldav: scan connection: %w", err)
		}
		a.collections = []string{bound}
		accounts = append(accounts, a)
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := rows.Err(); err != nil {
		return conn{}, false, err
	}
	if len(accounts) == 0 {
		c.logger.Warn("caldav: no CalDAV account is connected to move or cancel an event; it stays on its calendar",
			"user_id", userID)
		return conn{}, false, nil
	}

	// A second query, not a join, and only after the cursor above is closed: the pool is a
	// single connection.
	rows, err = c.db.QueryContext(ctx, `
		SELECT account_email, calendar_id FROM connection_calendars
		WHERE user_id = ? AND provider = 'caldav'`, userID)
	if err != nil {
		return conn{}, false, fmt.Errorf("caldav: load saved calendars: %w", err)
	}
	for rows.Next() {
		var email, calID string
		if err := rows.Scan(&email, &calID); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return conn{}, false, fmt.Errorf("caldav: scan saved calendar: %w", err)
		}
		for i := range accounts {
			if accounts[i].username == email {
				accounts[i].collections = append(accounts[i].collections, calID)
			}
		}
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := rows.Err(); err != nil {
		return conn{}, false, err
	}

	i, err := pickEventOwner(accounts, calendarID, eventID)
	if err != nil {
		return conn{}, false, err
	}
	a := accounts[i]
	pw, err := c.decrypt(a.pwEnc)
	if err != nil {
		return conn{}, false, fmt.Errorf("caldav: decrypt password: %w", err)
	}
	return conn{id: a.id, username: a.username, password: string(pw)}, true, nil
}

// pickEventOwner returns the index of the one account that holds eventID.
//
// An account can hold the event only if the event URL lies inside one of its collections: same
// scheme, host and port, and a path under the collection's at a segment boundary. That is what
// guarantees an account's credentials only go to a server that account is already configured to
// use. Among the accounts that pass, one whose collection is the calendar id recorded at creation
// wins; failing that (no id recorded, as for reassign and bookings that predate the column, or a
// calendar no longer saved) the account with the most specific collection does. A tie is refused
// rather than guessed.
func pickEventOwner(accounts []eventAccount, calendarID, eventID string) (int, error) {
	ev, ok := parseDAVURL(eventID)
	if !ok {
		return -1, errEventNotURL
	}
	cal, calOK := parseDAVURL(calendarID)

	best, tie := -1, false
	var bestExact bool
	bestDepth := -1
	for i, a := range accounts {
		held, exact, depth := false, false, -1
		for _, raw := range a.collections {
			col, ok := parseDAVURL(raw)
			if !ok || !col.contains(ev) {
				continue
			}
			held = true
			exact = exact || (calOK && col.sameCollection(cal))
			depth = max(depth, len(strings.TrimRight(col.path, "/")))
		}
		switch {
		case !held:
		case best < 0 || (exact && !bestExact) || (exact == bestExact && depth > bestDepth):
			best, bestExact, bestDepth, tie = i, exact, depth, false
		case exact == bestExact && depth == bestDepth:
			tie = true
		}
	}
	switch {
	case best < 0:
		return -1, errNoEventOwner
	case tie:
		return -1, errManyEventOwner
	}
	return best, nil
}

// davURL is the part of a CalDAV URL that decides where a request, and its credentials, go.
type davURL struct {
	scheme, host, port, path string
}

// parseDAVURL accepts only an absolute http(s) URL with a host. The port is made explicit and the
// host lower-cased, so equivalent spellings of one server compare equal. The path is kept in its
// escaped form, the form the event URL was built from.
func parseDAVURL(raw string) (davURL, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return davURL{}, false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return davURL{scheme: u.Scheme, host: strings.ToLower(u.Hostname()), port: port, path: u.EscapedPath()}, true
}

func (d davURL) sameOrigin(o davURL) bool {
	return d.scheme == o.scheme && d.host == o.host && d.port == o.port
}

// contains reports whether resource lies inside the collection d.
func (d davURL) contains(resource davURL) bool {
	dir := strings.TrimRight(d.path, "/") + "/"
	return d.sameOrigin(resource) && len(resource.path) > len(dir) && strings.HasPrefix(resource.path, dir)
}

func (d davURL) sameCollection(o davURL) bool {
	return d.sameOrigin(o) && strings.TrimRight(d.path, "/") == strings.TrimRight(o.path, "/")
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
