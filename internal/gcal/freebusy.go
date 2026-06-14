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
	TimeMin string               `json:"timeMin"`
	TimeMax string               `json:"timeMax"`
	Items   []map[string]string  `json:"items"`
}

type freeBusyResp struct {
	Calendars map[string]struct {
		Busy []struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"busy"`
	} `json:"calendars"`
}

// FreeBusy returns Google Calendar busy intervals for userID in [from, to).
// Only queries connections where check_conflicts = 1.
// Returns (nil, nil) if the user has no such connection.
func (c *Client) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	hc, calID, err := c.FreeBusyClient(ctx, userID)
	if err != nil || hc == nil {
		return nil, err
	}

	body, err := json.Marshal(freeBusyReq{
		TimeMin: from.UTC().Format(time.RFC3339),
		TimeMax: to.UTC().Format(time.RFC3339),
		Items:   []map[string]string{{"id": calID}},
	})
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/freeBusy", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
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
	// Iterate over all returned calendars (we requested one, so at most one entry).
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
