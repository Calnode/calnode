package caldav

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/calnode/calnode/internal/calendar"
)

// ListCalendars lists every calendar in the account that can hold events: each VEVENT-capable
// collection under the account's calendar home, on the same origin as that home (see
// listCollections). The collection the connection is bound to is marked Primary; Writable comes
// from the server's DAV:current-user-privilege-set, and counts as true when the server does not
// report one.
//
// The server URL typed at connect time is not stored, so discovery restarts from the bound
// collection (see homeForCalendar) using the stored credentials. Nothing new is persisted, which
// is why this needs no migration and works for connections made before calendars were listed.
//
// Every request made here stays on the bound collection's origin, and so does every calendar
// offered: this runs on each picker load and each saved selection, with the account's
// credentials, against URLs the server chose. homeForCalendar will not follow a principal or
// home on another origin, propfind refuses a redirect off it, and a collection off it is dropped
// below even if a listing somehow reached one.
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	cn, ok, err := c.accountConn(ctx, userID, accountEmail)
	if err != nil || !ok {
		return nil, err
	}
	home, err := c.homeForCalendar(ctx, cn)
	if err != nil {
		return nil, err
	}
	l, err := c.listCollections(ctx, cn.calURL, home, cn.username, cn.password)
	if err != nil {
		return nil, err
	}

	out := make([]calendar.CalendarInfo, 0, len(l.collections)+1)
	boundListed := false
	for _, col := range l.collections {
		if !sameOrigin(col.url, cn.calURL) {
			c.logger.Warn("caldav: skipping a calendar on another origin from the connected calendar", "connected", cn.calURL, "calendar", col.url)
			continue
		}
		primary := col.url == cn.calURL
		boundListed = boundListed || primary
		out = append(out, calendar.CalendarInfo{ID: col.url, Name: col.name(), Primary: primary, Writable: col.writable})
	}
	if !boundListed {
		// The bound collection is what free/busy reads until a selection is saved, and it is
		// already the URL this connection sends its credentials to, so it stays selectable even
		// when the home listing leaves it out (a home found by the parent fallback, say).
		// Nothing is known about it beyond what connect found, which is what this method
		// reported before calendars were listed.
		bound := calendar.CalendarInfo{ID: cn.calURL, Name: lastSegment(cn.calURL), Primary: true, Writable: true}
		out = append([]calendar.CalendarInfo{bound}, out...)
	}
	return out, nil
}

var _ calendar.SelectionValidator = (*Client)(nil)

// ValidateSelection accepts a selection only when every calendar id is one ListCalendars offers
// for the account right now. A CalDAV calendar id is the collection URL that free/busy and
// booking writes send the account's credentials to, so an id the client made up would bypass
// both the connect-time URL check and the same-origin rule applied when listing.
func (c *Client) ValidateSelection(ctx context.Context, userID, accountEmail string, calendarIDs []string) error {
	cals, err := c.ListCalendars(ctx, userID, accountEmail)
	if err != nil {
		return err
	}
	listed := make(map[string]bool, len(cals))
	for _, cal := range cals {
		listed[cal.ID] = true
	}
	for _, id := range calendarIDs {
		if !listed[id] {
			return &calendar.UnknownCalendarError{CalendarID: id}
		}
	}
	return nil
}

// homeForCalendar finds the calendar home holding the connection's bound collection.
// DAV:current-user-principal is defined on every resource (RFC 5397), and the principal names
// its calendar-home-set, so this is the same walk connect makes, started from the collection
// instead of the server URL. A server that answers neither gets the collection's parent, which is
// the calendar home on the usual layout (.../calendars/<user>/<calendar>/).
//
// A failed principal or home lookup falls through to the parent rather than failing, so a bound
// collection that has since been deleted does not stop the user choosing another one. An
// authentication failure still surfaces: the parent listing is made with the same credentials.
//
// A principal or home href on another origin from the bound collection is not followed, and
// neither is a redirect off it (propfind is pinned to the bound collection). Following either
// would send the account's credentials there on every picker load, and a home there would make
// that origin's collections look local to it. Both fall through to the parent, which is on the
// bound collection's origin by construction.
func (c *Client) homeForCalendar(ctx context.Context, cn conn) (string, error) {
	parent := parentCollection(cn.calURL)
	ms, reqURL, err := c.propfind(ctx, cn.calURL, cn.calURL, cn.username, cn.password, "0", propCurrentUserPrincipal)
	if err != nil {
		c.logger.Warn("caldav: principal lookup failed, listing the bound calendar's parent", "calendar", cn.calURL, "error", err)
		return parent, nil
	}
	var principal string
	for _, r := range ms.Responses {
		if h := r.okProp().CurrentUserPrincipal.Href; h != "" {
			principal = resolveRef(reqURL, h)
			break
		}
	}
	if principal == "" {
		return parent, nil
	}
	if !sameOrigin(principal, cn.calURL) {
		c.logger.Warn("caldav: not following a principal on another origin, listing the bound calendar's parent", "calendar", cn.calURL, "principal", principal)
		return parent, nil
	}
	home, err := c.calendarHome(ctx, cn.calURL, principal, cn.username, cn.password)
	switch {
	case err != nil:
		c.logger.Warn("caldav: calendar home lookup failed, listing the bound calendar's parent", "principal", principal, "error", err)
	case home == "":
	case !sameOrigin(home, cn.calURL):
		c.logger.Warn("caldav: not following a calendar home on another origin, listing the bound calendar's parent", "calendar", cn.calURL, "home", home)
	default:
		return home, nil
	}
	return parent, nil
}

// accountConn loads one account's connection (by account email) regardless of its
// check_conflicts / is_destination flags. ok=false when the user has no such account.
func (c *Client) accountConn(ctx context.Context, userID, accountEmail string) (conn, bool, error) {
	var cn conn
	var pwEnc string
	err := c.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(account_email,''), access_token_enc, calendar_id
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'caldav' AND COALESCE(account_email,'') = ?
		LIMIT 1`, userID, accountEmail).Scan(&cn.id, &cn.username, &pwEnc, &cn.calURL)
	if err == sql.ErrNoRows {
		return conn{}, false, nil
	}
	if err != nil {
		return conn{}, false, fmt.Errorf("caldav: load account connection: %w", err)
	}
	pw, err := c.decrypt(pwEnc)
	if err != nil {
		return conn{}, false, fmt.Errorf("caldav: decrypt password: %w", err)
	}
	cn.password = string(pw)
	return cn, true, nil
}
