package gcal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

type freeBusyReq struct {
	TimeMin string              `json:"timeMin"`
	TimeMax string              `json:"timeMax"`
	Items   []map[string]string `json:"items"`
}

type freeBusyResp struct {
	Calendars map[string]struct {
		Busy []struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"busy"`
	} `json:"calendars"`
}

// FreeBusy returns the UNION of Google Calendar busy intervals for userID in [from, to)
// across every connected Google account with check_conflicts = 1 (so all of a user's
// connected calendars are honoured, not just one). Returns (nil, nil) if the user has no such
// connection. Fail-open: a single account that errors is logged and skipped, so a flaky
// connection never blocks availability or a booking.
func (c *Client) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	conns, err := c.freeBusyConnections(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []slots.Interval
	for _, conn := range conns {
		intervals, err := c.freeBusyForConn(ctx, conn, from, to)
		if err != nil {
			c.logger.Warn("gcal: freebusy for connection failed, skipping", "user_id", userID, "error", err)
			continue
		}
		out = append(out, intervals...)
	}
	return out, nil
}

// freeBusyForConn queries one connection's selected calendars for busy intervals in [from, to).
// Google's freeBusy accepts multiple items and returns each calendar's busy blocks, which the
// decoder below unions.
func (c *Client) freeBusyForConn(ctx context.Context, conn fbConn, from, to time.Time) ([]slots.Interval, error) {
	items := make([]map[string]string, 0, len(conn.calIDs))
	for _, id := range conn.calIDs {
		items = append(items, map[string]string{"id": id})
	}
	body, err := json.Marshal(freeBusyReq{
		TimeMin: from.UTC().Format(time.RFC3339),
		TimeMax: to.UTC().Format(time.RFC3339),
		Items:   items,
	})
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/freeBusy", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := conn.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gcal: freeBusy status %d", resp.StatusCode)
	}

	var fbr freeBusyResp
	if err := json.NewDecoder(resp.Body).Decode(&fbr); err != nil {
		return nil, fmt.Errorf("gcal: freeBusy decode: %w", err)
	}
	var out []slots.Interval
	for _, cal := range fbr.Calendars {
		for _, b := range cal.Busy {
			s, err1 := time.Parse(time.RFC3339, b.Start)
			e, err2 := time.Parse(time.RFC3339, b.End)
			if err1 != nil || err2 != nil {
				c.logger.Warn("gcal: skipping unparseable busy interval", "start", b.Start, "end", b.End)
				continue
			}
			out = append(out, slots.Interval{Start: s, End: e})
		}
	}
	return out, nil
}
