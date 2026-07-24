package caldav

import (
	"context"
	"database/sql"

	"github.com/calnode/calnode/internal/calendar"
)

// ListCalendars returns the connected CalDAV calendar for the account. Discovery currently
// binds one calendar per connection; enumerating multiple CalDAV collections is a future
// enhancement, so this reports the single bound calendar.
func (c *Client) ListCalendars(ctx context.Context, userID, accountEmail string) ([]calendar.CalendarInfo, error) {
	var calID string
	err := c.db.QueryRowContext(ctx, `
		SELECT calendar_id FROM calendar_connections
		WHERE user_id = ? AND provider = 'caldav' AND COALESCE(account_email,'') = ?
		LIMIT 1`, userID, accountEmail).Scan(&calID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []calendar.CalendarInfo{{ID: calID, Name: "Calendar", Primary: true}}, nil
}
