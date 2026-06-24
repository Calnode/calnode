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
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:resourcetype/><D:displayname/><C:supported-calendar-component-set/></D:prop></D:propfind>`
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
// of the first VEVENT-capable calendar collection.
func (c *Client) discoverCalendar(ctx context.Context, serverURL, username, password string) (string, error) {
	// 1. current-user-principal — try the base URL, then the RFC 5785 well-known path.
	principal, base, err := c.findPrincipal(ctx, serverURL, username, password)
	if err != nil {
		return "", err
	}

	// 2. calendar-home-set on the principal.
	ms, homeReqURL, err := c.propfind(ctx, principal, username, password, "0", propCalendarHomeSet)
	if err != nil {
		return "", err
	}
	var home string
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

	// 3. list calendar collections (Depth 1) and pick the first that supports VEVENT.
	ms, homeReqURL, err = c.propfind(ctx, home, username, password, "1", propCalendarCollections)
	if err != nil {
		return "", err
	}
	var firstCal string
	for _, r := range ms.Responses {
		p := r.okProp()
		if p.ResourceType.Calendar == nil {
			continue // not a calendar collection (e.g. the home container itself)
		}
		if !supportsVEvent(p.SupportedComps) {
			continue // e.g. a tasks/reminders or birthdays calendar
		}
		u := resolveRef(homeReqURL, r.Href)
		if firstCal == "" {
			firstCal = u
		}
		// Prefer a calendar literally named/pathed "calendar" as the default when present.
		if strings.EqualFold(p.DisplayName, "Calendar") || strings.Contains(strings.ToLower(u), "/calendar") {
			return u, nil
		}
	}
	if firstCal == "" {
		_ = base
		return "", fmt.Errorf("caldav: no writable calendar found on the server")
	}
	return firstCal, nil
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
