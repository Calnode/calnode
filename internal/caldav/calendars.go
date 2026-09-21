package caldav

import (
	"context"
	"database/sql"
	"path"
	"strings"

	"github.com/calnode/calnode/internal/calendar"
)

// ListCalendars returns every VEVENT-capable collection on the connected CalDAV account,
// with server-side display names. The listing is live (a Depth-1 PROPFIND on the calendar
// home stored at connect time), so calendars added server-side show up without reconnecting.
// When the home URL is unknown (connected before it was stored) or the server can't be
// reached, it falls back to the seeded connection_calendars rows — the picker keeps working
// offline, just without newly added collections.
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	var username, pwEnc, boundCalURL, homeURL string
	err := c.db.QueryRowContext(ctx, `
		SELECT COALESCE(account_email,''), access_token_enc, calendar_id, COALESCE(caldav_home_url,'')
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'caldav' AND COALESCE(account_email,'') = ?
		LIMIT 1`, userID, accountEmail).Scan(&username, &pwEnc, &boundCalURL, &homeURL)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if homeURL != "" {
		if cals, lerr := c.listLive(ctx, username, pwEnc, boundCalURL, homeURL); lerr == nil {
			return cals, nil
		} else {
			c.logger.Warn("caldav: live calendar list failed, using stored selection", "user_id", userID, "error", lerr)
		}
	}
	return c.listStored(ctx, userID, accountEmail, boundCalURL)
}

// listLive PROPFINDs the calendar home and maps every VEVENT-capable collection.
func (c *Client) listLive(ctx context.Context, username, pwEnc, boundCalURL, homeURL string) ([]calendar.CalendarInfo, error) {
	pw, err := c.decrypt(pwEnc)
	if err != nil {
		return nil, err
	}
	ms, reqURL, err := c.propfind(ctx, homeURL, username, string(pw), "1", propCalendarCollections)
	if err != nil {
		return nil, err
	}
	var out []calendar.CalendarInfo
	for _, r := range ms.Responses {
		p := r.okProp()
		if p.ResourceType.Calendar == nil {
			continue
		}
		if !supportsVEvent(p.SupportedComps) {
			continue
		}
		u := resolveRef(reqURL, r.Href)
		name := strings.TrimSpace(p.DisplayName)
		if name == "" {
			name = path.Base(strings.TrimSuffix(u, "/"))
			if name == "" || name == "." || name == "/" {
				name = "Calendar"
			}
		}
		// CalDAV exposes exactly the collections the user configured, and they configured
		// them precisely so Calnode could write there - so unlike Google/Microsoft there is
		// no read-only case to detect here.
		out = append(out, calendar.CalendarInfo{ID: u, Name: name, Primary: u == boundCalURL, Writable: true})
	}
	return out, nil
}

// listStored returns the seeded connection_calendars rows: same shape as the live listing,
// minus collections added server-side after connecting.
func (c *Client) listStored(ctx context.Context, userID, accountEmail, boundCalURL string) ([]calendar.CalendarInfo, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT calendar_id, name FROM connection_calendars
		WHERE user_id = ? AND provider = 'caldav' AND account_email = ?
		ORDER BY calendar_id`, userID, accountEmail)
	if err != nil {
		return nil, err
	}
	var out []calendar.CalendarInfo
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, err
		}
		if strings.TrimSpace(name) == "" {
			name = path.Base(strings.TrimSuffix(id, "/"))
			if name == "" || name == "." || name == "/" {
				name = "Calendar"
			}
		}
		out = append(out, calendar.CalendarInfo{ID: id, Name: name, Primary: id == boundCalURL, Writable: true})
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out = []calendar.CalendarInfo{{ID: boundCalURL, Name: "Calendar", Primary: true, Writable: true}}
	}
	return out, nil
}
