package caldav

import (
	"context"
	"fmt"
	"strings"
)

// Preset server base URLs for the well-known CalDAV providers, so the UI can offer a simple
// picker. "custom"/Nextcloud users supply their own full base URL (e.g. a Nextcloud
// https://host/remote.php/dav). Discovery follows redirects and the standard principal →
// calendar-home → calendar-collection walk, so a precise URL isn't required.
var Presets = map[string]string{
	"icloud":   "https://caldav.icloud.com",
	"fastmail": "https://caldav.fastmail.com",
}

// propfind bodies for each discovery step.
const (
	propCurrentUserPrincipal = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:"><D:prop><D:current-user-principal/></D:prop></D:propfind>`

	propCalendarHomeSet = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><C:calendar-home-set/></D:prop></D:propfind>`

	propCalendarCollections = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:resourcetype/><D:displayname/><C:supported-calendar-component-set/><D:current-user-privilege-set/></D:prop></D:propfind>`
)

// Connect validates the given CalDAV credentials by discovering the user's default calendar
// collection, then stores the connection (encrypting the app password). Returns the resolved
// account email and calendar URL. The serverURL is a base (a Presets value or a user-supplied
// Nextcloud/custom URL); discovery resolves the rest. An auth failure or a server with no
// VEVENT-capable calendar is surfaced as an error so the connect form can show it.
func (c *Client) Connect(ctx context.Context, userID, serverURL, username, password string) (accountEmail, calURL string, err error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || strings.TrimSpace(serverURL) == "" {
		return "", "", fmt.Errorf("caldav: server URL, username and app password are all required")
	}
	calURL, err = c.discoverCalendar(ctx, strings.TrimSpace(serverURL), username, password)
	if err != nil {
		return "", "", err
	}
	accountEmail = strings.ToLower(username)
	if err := c.saveConnection(ctx, userID, accountEmail, password, calURL); err != nil {
		return "", "", err
	}
	return accountEmail, calURL, nil
}

// discoverCalendar walks principal → calendar-home → calendar collections and returns the URL
// of the collection the connection binds to: the account's default, which free/busy reads and
// bookings are written to until the user picks calendars of their own.
func (c *Client) discoverCalendar(ctx context.Context, serverURL, username, password string) (string, error) {
	// 1. current-user-principal — try the base URL, then the RFC 5785 well-known path.
	principal, _, err := c.findPrincipal(ctx, serverURL, username, password)
	if err != nil {
		return "", err
	}

	// 2. calendar-home-set on the principal.
	home, err := c.calendarHome(ctx, "", principal, username, password)
	if err != nil {
		return "", err
	}
	if home == "" {
		// Some servers expose the calendar home at the principal itself.
		home = principal
	}

	// 3. list calendar collections (Depth 1) and pick the default among them.
	l, err := c.listCollections(ctx, "", home, username, password)
	if err != nil {
		return "", err
	}
	cols := l.collections
	if len(cols) == 0 && len(l.foreign) > 0 {
		// Every event calendar was on another origin. That is the signature of a self-hosted
		// server behind a reverse proxy that reports its internal scheme or host in hrefs, and
		// "no calendar found" would send the operator looking in the wrong place.
		return "", fmt.Errorf("caldav: the server lists its calendars at a different address (%s) than the one Calnode reached it at (%s); check the server's reverse proxy or base URL settings",
			displayOrigin(l.foreign[0]), displayOrigin(l.homeURL))
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("caldav: no writable calendar found on the server")
	}
	for _, col := range cols {
		// Prefer a calendar literally named/pathed "calendar" as the default when present.
		if strings.EqualFold(col.displayName, "Calendar") || strings.Contains(strings.ToLower(col.url), "/calendar") {
			return col.url, nil
		}
	}
	return cols[0].url, nil
}

// calendarHome returns the calendar-home-set a principal reports (RFC 4791 §6.2.1), or "" when
// it reports none. pinOrigin is passed to propfind.
func (c *Client) calendarHome(ctx context.Context, pinOrigin, principal, username, password string) (string, error) {
	ms, reqURL, err := c.propfind(ctx, pinOrigin, principal, username, password, "0", propCalendarHomeSet)
	if err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		if h := r.okProp().CalendarHomeSet.Href; h != "" {
			return resolveRef(reqURL, h), nil
		}
	}
	return "", nil
}

// collection is one calendar collection that can hold events, as listed under a calendar home.
type collection struct {
	url         string
	displayName string // as reported; "" when the server gave none
	writable    bool
}

// name is what the calendar picker shows: the display name, else the last path segment.
func (col collection) name() string {
	if n := strings.TrimSpace(col.displayName); n != "" {
		return n
	}
	return lastSegment(col.url)
}

// listing is what one Depth 1 PROPFIND of a calendar home returned.
type listing struct {
	homeURL     string       // the URL that answered, after any redirects
	collections []collection // same-origin, VEVENT-capable, in server order
	foreign     []string     // VEVENT-capable collections skipped for being on another origin
}

// listCollections lists the VEVENT-capable calendar collections directly under a calendar home,
// in the order the server returns them.
//
// A collection whose URL is on a different origin (scheme, host, port) from the home URL that
// listed it is logged and skipped. Every URL returned here can be stored as a calendar id, and a
// stored id is then sent the account's Basic credentials on every free/busy read and booking
// write. The server already sees those credentials; an href must not be able to make them go to
// some other host for as long as the selection is saved. Skipped URLs are reported in foreign so
// connect can say why it found nothing. pinOrigin is passed to propfind.
func (c *Client) listCollections(ctx context.Context, pinOrigin, home, username, password string) (listing, error) {
	ms, homeReqURL, err := c.propfind(ctx, pinOrigin, home, username, password, "1", propCalendarCollections)
	if err != nil {
		return listing{}, err
	}
	l := listing{homeURL: homeReqURL}
	seen := map[string]bool{}
	for _, r := range ms.Responses {
		p := r.okProp()
		if p.ResourceType.Calendar == nil {
			continue // not a calendar collection (e.g. the home container itself)
		}
		if !supportsVEvent(p.SupportedComps) {
			continue // e.g. a tasks/reminders list
		}
		u := resolveRef(homeReqURL, r.Href)
		if !sameOrigin(u, homeReqURL) {
			c.logger.Warn("caldav: skipping a calendar listed on another origin", "home", homeReqURL, "calendar", u)
			l.foreign = append(l.foreign, u)
			continue
		}
		if seen[u] {
			continue // the picker keys rows by id, and the selection table is unique on it
		}
		seen[u] = true
		l.collections = append(l.collections, collection{url: u, displayName: p.DisplayName, writable: p.PrivilegeSet.canWrite()})
	}
	return l, nil
}

// findPrincipal resolves current-user-principal, trying the base URL first and then the
// RFC 5785 well-known CalDAV path. Returns the principal URL and the base it was found under.
func (c *Client) findPrincipal(ctx context.Context, serverURL, username, password string) (principal, base string, err error) {
	candidates := []string{serverURL}
	if !strings.Contains(serverURL, "/.well-known/") {
		candidates = append(candidates, strings.TrimRight(serverURL, "/")+"/.well-known/caldav")
	}
	var lastErr error
	for _, cand := range candidates {
		ms, reqURL, err := c.propfind(ctx, "", cand, username, password, "0", propCurrentUserPrincipal)
		if err != nil {
			lastErr = err
			continue
		}
		for _, r := range ms.Responses {
			if h := r.okProp().CurrentUserPrincipal.Href; h != "" {
				return resolveRef(reqURL, h), reqURL, nil
			}
		}
		// No principal in the response but the PROPFIND worked — use the resolved URL itself.
		lastErr = fmt.Errorf("caldav: server did not return a user principal")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("caldav: could not reach the CalDAV server")
	}
	return "", "", lastErr
}

// supportsVEvent reports whether a calendar collection accepts VEVENT components. An empty
// component set (server didn't report one) is treated as capable.
func supportsVEvent(s supportedCompSet) bool {
	if len(s.Comps) == 0 {
		return true
	}
	for _, comp := range s.Comps {
		if strings.EqualFold(comp.Name, "VEVENT") {
			return true
		}
	}
	return false
}
