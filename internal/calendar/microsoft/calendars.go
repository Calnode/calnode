package microsoft

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calnode/calnode/internal/calendar"
)

// clientForAccount builds an authorized Graph client for one connected account
// (by account email). Returns (nil, nil) when the user has no such connection.
func (c *Client) clientForAccount(ctx context.Context, userID, accountEmail string) (*http.Client, error) {
	var accessEnc, refreshEnc, calID, expiryStr, email string
	err := c.db.QueryRowContext(ctx, `
		SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,''), COALESCE(account_email,'')
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'microsoft' AND COALESCE(account_email,'') = ?
		LIMIT 1`, userID, accountEmail).Scan(&accessEnc, &refreshEnc, &calID, &expiryStr, &email)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("microsoft: load account connection: %w", err)
	}
	return c.buildClient(ctx, userID, accessEnc, refreshEnc, calID, expiryStr, email)
}

type msCalListResp struct {
	Value []struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		IsDefaultCalendar bool   `json:"isDefaultCalendar"`
	} `json:"value"`
}

// ListCalendars returns the calendars in the account (Graph GET /me/calendars).
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	hc, err := c.clientForAccount(ctx, userID, accountEmail)
	if err != nil || hc == nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiBase+"/me/calendars?$select=id,name,isDefaultCalendar&$top=100", nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("microsoft: list calendars call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("microsoft: list calendars status %d", resp.StatusCode)
	}
	var lr msCalListResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("microsoft: list calendars decode: %w", err)
	}
	out := make([]calendar.CalendarInfo, 0, len(lr.Value))
	for _, it := range lr.Value {
		name := it.Name
		if name == "" {
			name = it.ID
		}
		out = append(out, calendar.CalendarInfo{ID: it.ID, Name: name, Primary: it.IsDefaultCalendar})
	}
	return out, nil
}
