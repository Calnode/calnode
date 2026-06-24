package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// calendarQuery is the REPORT body: fetch the calendar-data for every VEVENT overlapping the
// window, asking the server to EXPAND recurrences into concrete instances (<C:expand>) so we
// never have to interpret RRULE client-side.
const calendarQueryTmpl = `<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <C:calendar-data>
      <C:expand start="%[1]s" end="%[2]s"/>
    </C:calendar-data>
  </D:prop>
  <C:filter>
    <C:comp-filter name="VCALENDAR">
      <C:comp-filter name="VEVENT">
        <C:time-range start="%[1]s" end="%[2]s"/>
      </C:comp-filter>
    </C:comp-filter>
  </C:filter>
</C:calendar-query>`

// FreeBusy returns the union of busy intervals across every CalDAV account the user has
// connected with check_conflicts = 1. Each account is queried with a CalDAV calendar-query
// REPORT; transparent and cancelled events don't block. Fail-open per account: a connection
// that errors is logged and skipped (a flaky calendar never blocks availability).
func (c *Client) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	conns, err := c.conflictConns(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return nil, nil
	}
	body := fmt.Sprintf(calendarQueryTmpl, icsUTC(from), icsUTC(to))
	var out []slots.Interval
	for _, cn := range conns {
		iv, err := c.freeBusyForConn(ctx, cn, body)
		if err != nil {
			c.logger.Warn("caldav: freebusy connection failed", "user_id", userID, "calendar", cn.calURL, "error", err)
			continue
		}
		out = append(out, iv...)
	}
	return out, nil
}

// freeBusyForConn runs the REPORT against one calendar collection and parses the busy intervals.
func (c *Client) freeBusyForConn(ctx context.Context, cn conn, body string) ([]slots.Interval, error) {
	status, b, _, err := c.do(ctx, "REPORT", cn.calURL, cn.username, cn.password, "1", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusMultiStatus && status != http.StatusOK {
		return nil, fmt.Errorf("caldav: REPORT returned status %d", status)
	}
	var ms msMultistatus
	if err := xml.Unmarshal(b, &ms); err != nil {
		return nil, fmt.Errorf("caldav: parse REPORT response: %w", err)
	}
	var out []slots.Interval
	for _, r := range ms.Responses {
		data := strings.TrimSpace(r.okProp().CalendarData)
		if data == "" {
			continue
		}
		for _, ev := range parseVEvents(data) {
			if ev.transparent || ev.cancelled || ev.start.IsZero() || !ev.end.After(ev.start) {
				continue
			}
			out = append(out, slots.Interval{Start: ev.start, End: ev.end})
		}
	}
	return out, nil
}

// icsUTC formats a time as an iCalendar UTC timestamp (20060102T150405Z).
func icsUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}
