package caldav

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/calnode/calnode/internal/uid"
)

// discoveredCalendar is one VEVENT-capable collection found on the server.
type discoveredCalendar struct {
	URL  string // absolute collection URL
	Name string // displayname, or the last path segment when the server sends none
}

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
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:resourcetype/><D:displayname/><C:supported-calendar-component-set/></D:prop></D:propfind>`
)

// Connect validates the given CalDAV credentials by discovering the user's calendar
// collections, then stores the connection (encrypting the app password) bound to the
// default collection. Every discovered collection is seeded into connection_calendars
// so the calendar picker lists them all with their server-side names. Returns the
// resolved account email and calendar URL. The serverURL is a base (a Presets value or
// a user-supplied Nextcloud/custom URL); discovery resolves the rest. An auth failure
// or a server with no VEVENT-capable calendar is surfaced as an error so the connect
// form can show it.
func (c *Client) Connect(ctx context.Context, userID, serverURL, username, password string) (accountEmail, calURL string, err error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || strings.TrimSpace(serverURL) == "" {
		return "", "", fmt.Errorf("caldav: server URL, username and app password are all required")
	}
	home, cals, err := c.discoverCalendars(ctx, strings.TrimSpace(serverURL), username, password)
	if err != nil {
		return "", "", err
	}
	calURL = preferDefault(cals)
	accountEmail = strings.ToLower(username)
	if err := c.saveConnection(ctx, userID, accountEmail, password, calURL, home); err != nil {
		return "", "", err
	}
	if err := c.seedDiscoveredCalendars(ctx, userID, accountEmail, calURL, cals); err != nil {
		return "", "", err
	}
	return accountEmail, calURL, nil
}

// preferDefault picks the bound collection out of the discovered set: a calendar
// literally named/pathed "calendar" wins when present, else the first one. Discovery
// guarantees at least one entry.
func preferDefault(cals []discoveredCalendar) string {
	first := cals[0].URL
	for _, cal := range cals {
		if strings.EqualFold(cal.Name, "Calendar") || strings.Contains(strings.ToLower(cal.URL), "/calendar") {
			return cal.URL
		}
	}
	return first
}

// discoverCalendars walks principal → calendar-home → calendar collections and returns
// the home URL plus every VEVENT-capable collection, with server-side display names.
func (c *Client) discoverCalendars(ctx context.Context, serverURL, username, password string) (home string, cals []discoveredCalendar, err error) {
	// 1. current-user-principal — try the base URL, then the RFC 5785 well-known path.
	principal, base, err := c.findPrincipal(ctx, serverURL, username, password)
	if err != nil {
		return "", nil, err
	}

	// 2. calendar-home-set on the principal.
	ms, homeReqURL, err := c.propfind(ctx, principal, username, password, "0", propCalendarHomeSet)
	if err != nil {
		return "", nil, err
	}
	for _, r := range ms.Responses {
		if h := r.okProp().CalendarHomeSet.Href; h != "" {
			home = resolveRef(homeReqURL, h)
			break
		}
	}
	if home == "" {
		// Some servers expose the calendar home at the principal itself.
		home = principal
	}

	// 3. list calendar collections (Depth 1), keeping every VEVENT-capable one.
	ms, homeReqURL, err = c.propfind(ctx, home, username, password, "1", propCalendarCollections)
	if err != nil {
		return "", nil, err
	}
	for _, r := range ms.Responses {
		p := r.okProp()
		if p.ResourceType.Calendar == nil {
			continue // not a calendar collection (e.g. the home container itself)
		}
		if !supportsVEvent(p.SupportedComps) {
			continue // e.g. a tasks/reminders or birthdays calendar
		}
		u := resolveRef(homeReqURL, r.Href)
		name := strings.TrimSpace(p.DisplayName)
		if name == "" {
			name = path.Base(strings.TrimSuffix(u, "/"))
			if name == "" || name == "." || name == "/" {
				name = "Calendar"
			}
		}
		cals = append(cals, discoveredCalendar{URL: u, Name: name})
	}
	if len(cals) == 0 {
		_ = base
		return "", nil, fmt.Errorf("caldav: no writable calendar found on the server")
	}
	return home, cals, nil
}

// seedDiscoveredCalendars records every discovered collection in connection_calendars
// so the calendar picker lists them all immediately with server-side names. The bound
// (default) collection is seeded conflict-checked, preserving the pre-picker behaviour
// where a fresh connection checks its one calendar; the rest start unchecked for the
// user to tick. Names refresh on every (re-)connect; the user's check_conflicts /
// is_destination flags are preserved (only name is overwritten on conflict) — a
// reconnect never resets the picker.
func (c *Client) seedDiscoveredCalendars(ctx context.Context, userID, accountEmail, boundCalURL string, cals []discoveredCalendar) error {
	for _, cal := range cals {
		check := 0
		if cal.URL == boundCalURL {
			check = 1
		}
		if _, err := c.db.ExecContext(ctx, `
			INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, name, check_conflicts, is_destination)
			VALUES (?, ?, 'caldav', ?, ?, ?, ?, 0)
			ON CONFLICT(user_id, provider, account_email, calendar_id) DO UPDATE SET name = excluded.name`,
			uid.New(), userID, accountEmail, cal.URL, cal.Name, check); err != nil {
			return fmt.Errorf("caldav: seed calendars: %w", err)
		}
	}
	return nil
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
		ms, reqURL, err := c.propfind(ctx, cand, username, password, "0", propCurrentUserPrincipal)
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
