package microsoft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// graphDateTime is Graph's start/end shape: an ISO-8601 wall-clock string plus a
// timezone name. With the Prefer: outlook.timezone="UTC" header, values come back
// in UTC without an offset, e.g. "2026-06-22T21:00:00.0000000".
type graphDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// parse interprets the (offset-less, UTC) Graph dateTime.
func (g graphDateTime) parse() (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05.999999999", g.DateTime)
}

type calendarViewResp struct {
	Value []struct {
		Start graphDateTime `json:"start"`
		End   graphDateTime `json:"end"`
	} `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

// FreeBusy returns the UNION of busy intervals for userID in [from, to) across every
// connected Microsoft account with check_conflicts = 1 (every event in the window counts as
// busy). Returns (nil, nil) if the user has no such connection. Fail-open: an account that
// errors is logged and skipped.
func (c *Client) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	clients, err := c.freeBusyConnections(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []slots.Interval
	for _, hc := range clients {
		intervals, err := c.freeBusyForClient(ctx, hc, from, to)
		if err != nil {
			c.logger.Warn("microsoft: freebusy for connection failed, skipping", "user_id", userID, "error", err)
			continue
		}
		out = append(out, intervals...)
	}
	return out, nil
}

// freeBusyForClient reads one account's calendar view for busy intervals in [from, to).
func (c *Client) freeBusyForClient(ctx context.Context, hc *http.Client, from, to time.Time) ([]slots.Interval, error) {
	q := url.Values{}
	q.Set("startDateTime", from.UTC().Format(time.RFC3339))
	q.Set("endDateTime", to.UTC().Format(time.RFC3339))
	q.Set("$select", "start,end")
	q.Set("$top", "200")
	next := c.apiBase + "/me/calendarView?" + q.Encode()

	var out []slots.Interval
	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, fmt.Errorf("microsoft: calendarView request: %w", err)
		}
		req.Header.Set("Prefer", `outlook.timezone="UTC"`)

		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("microsoft: calendarView call: %w", err)
		}
		var cv calendarViewResp
		if resp.StatusCode != http.StatusOK {
			msg := graphErrBody(resp)
			resp.Body.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
			return nil, fmt.Errorf("microsoft: calendarView status %d: %s", resp.StatusCode, msg)
		}
		derr := json.NewDecoder(resp.Body).Decode(&cv)
		resp.Body.Close() // #nosec G104 -- body already decoded above; nothing actionable on close error
		if derr != nil {
			return nil, fmt.Errorf("microsoft: calendarView decode: %w", derr)
		}
		for _, ev := range cv.Value {
			s, err1 := ev.Start.parse()
			e, err2 := ev.End.parse()
			if err1 != nil || err2 != nil {
				c.logger.Warn("microsoft: skipping unparseable busy interval", "start", ev.Start.DateTime, "end", ev.End.DateTime)
				continue
			}
			out = append(out, slots.Interval{Start: s.UTC(), End: e.UTC()})
		}
		next = cv.NextLink
	}
	return out, nil
}
