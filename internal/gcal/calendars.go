package gcal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

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
	} `json:"items"`
}

// ListCalendars returns every calendar in the account (calendarList.list). Read-only
// calendars are included — they're valid for conflict checks even if not writable.
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	hc, err := c.clientForAccount(ctx, userID, accountEmail)
	if err != nil || hc == nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiBase+"/users/me/calendarList?maxResults=250", nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcal: calendarList call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gcal: calendarList status %d", resp.StatusCode)
	}
	var lr calListResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("gcal: calendarList decode: %w", err)
	}
	out := make([]calendar.CalendarInfo, 0, len(lr.Items))
	for _, it := range lr.Items {
		if it.Deleted {
			continue
		}
		name := it.Summary
		if name == "" {
			name = it.ID
		}
		out = append(out, calendar.CalendarInfo{ID: it.ID, Name: name, Primary: it.Primary})
	}
	return out, nil
}
