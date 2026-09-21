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

// msCalListResp is the GET /me/calendars response. Every property decoded here must also
// be named in ListCalendars' $select: Graph returns only the selected properties, and an
// absent bool decodes as false without error.
// TestListCalendars_selectNamesEveryDecodedProperty holds the two together.
// NextLink is exempt: @odata.nextLink is response metadata, returned regardless of
// $select, and the guard test only inspects the Value item struct.
type msCalListResp struct {
	Value []struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		IsDefaultCalendar bool   `json:"isDefaultCalendar"`
		// Graph reports this per calendar; false for one shared with the user read-only.
		CanEdit bool `json:"canEdit"`
	} `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

// ListCalendars returns the calendars in the account (Graph GET /me/calendars),
// following @odata.nextLink until every page is read. Without this, accounts with
// more than $top calendars silently lose the rest: they can't be checked for
// conflicts or chosen as the destination.
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	hc, err := c.clientForAccount(ctx, userID, accountEmail)
	if err != nil || hc == nil {
		return nil, err
	}
	next := c.apiBase + "/me/calendars?$select=id,name,isDefaultCalendar,canEdit&$top=100"
	var out []calendar.CalendarInfo
	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("microsoft: list calendars call: %w", err)
		}
		var lr msCalListResp
		if resp.StatusCode != http.StatusOK {
			msg := graphErrBody(resp)
			resp.Body.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
			return nil, fmt.Errorf("microsoft: list calendars status %d: %s", resp.StatusCode, msg)
		}
		derr := json.NewDecoder(resp.Body).Decode(&lr)
		resp.Body.Close() // #nosec G104 -- body already decoded above; nothing actionable on close error
		if derr != nil {
			return nil, fmt.Errorf("microsoft: list calendars decode: %w", derr)
		}
		for _, it := range lr.Value {
			name := it.Name
			if name == "" {
				name = it.ID
			}
			out = append(out, calendar.CalendarInfo{
				ID: it.ID, Name: name, Primary: it.IsDefaultCalendar, Writable: it.CanEdit,
			})
		}
		// The nextLink is a complete Graph URL (same host, skiptoken query) — follow
		// it verbatim, the way calendarView follows its NextLink.
		next = lr.NextLink
	}
	return out, nil
}
