package gcal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/calnode/calnode/internal/calendar"
)

// clientForAccount builds an authorized HTTP client for one connected Google account
// (by account email). Returns (nil, nil) when the user has no such connection.
func (c *Client) clientForAccount(ctx context.Context, userID, accountEmail string) (*http.Client, error) {
	var accessEnc, refreshEnc, calID, expiryStr, email string
	err := c.db.QueryRowContext(ctx, `
		SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,''), COALESCE(account_email,'')
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'google' AND COALESCE(account_email,'') = ?
		LIMIT 1`, userID, accountEmail).Scan(&accessEnc, &refreshEnc, &calID, &expiryStr, &email)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gcal: load account connection: %w", err)
	}
	return c.buildClient(ctx, userID, accessEnc, refreshEnc, calID, expiryStr, email)
}

type calListResp struct {
	Items []struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Primary bool   `json:"primary"`
		Deleted bool   `json:"deleted"`
		// "owner" | "writer" | "reader" | "freeBusyReader"
		AccessRole string `json:"accessRole"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

// ListCalendars returns every calendar in the account (calendarList.list), following
// nextPageToken until every page is read. Without this, accounts subscribed to more
// than maxResults calendars silently lose the rest. Read-only calendars are included
// — they're valid for conflict checks even if not writable.
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	hc, err := c.clientForAccount(ctx, userID, accountEmail)
	if err != nil || hc == nil {
		return nil, err
	}
	var out []calendar.CalendarInfo
	pageToken := ""
	for {
		endpoint := c.apiBase + "/users/me/calendarList?maxResults=250"
		if pageToken != "" {
			endpoint += "&pageToken=" + url.QueryEscape(pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gcal: calendarList call: %w", err)
		}
		var lr calListResp
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close() // #nosec G104 -- already returning the status; nothing actionable on close error
			return nil, fmt.Errorf("gcal: calendarList status %d", resp.StatusCode)
		}
		derr := json.NewDecoder(resp.Body).Decode(&lr)
		resp.Body.Close() // #nosec G104 -- body already decoded above; nothing actionable on close error
		if derr != nil {
			return nil, fmt.Errorf("gcal: calendarList decode: %w", derr)
		}
		for _, it := range lr.Items {
			if it.Deleted {
				continue
			}
			name := it.Summary
			if name == "" {
				name = it.ID
			}
			out = append(out, calendar.CalendarInfo{
				ID: it.ID, Name: name, Primary: it.Primary,
				Writable: it.AccessRole == "owner" || it.AccessRole == "writer",
			})
		}
		if lr.NextPageToken == "" {
			break
		}
		pageToken = lr.NextPageToken
	}
	return out, nil
}
