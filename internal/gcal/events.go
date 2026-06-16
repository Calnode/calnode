package gcal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// CreateEventParams holds the data needed to create a calendar event.
type CreateEventParams struct {
	Summary        string
	Description    string
	Start, End     time.Time
	OrganizerName  string
	OrganizerEmail string
}

type calEventDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type calEventAttendee struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
}

type calEventReq struct {
	Summary     string             `json:"summary"`
	Description string             `json:"description,omitempty"`
	Start       calEventDateTime   `json:"start"`
	End         calEventDateTime   `json:"end"`
	Attendees   []calEventAttendee `json:"attendees"`
}

type calEventResp struct {
	ID string `json:"id"`
}

// CreateEvent creates a Google Calendar event and returns the event ID.
// Returns ("", nil) if the user has no is_destination connection.
func (c *Client) CreateEvent(ctx context.Context, userID string, p CreateEventParams) (string, error) {
	hc, calID, err := c.DestinationClient(ctx, userID)
	if err != nil || hc == nil {
		return "", err
	}

	attendees := []calEventAttendee{}
	if p.OrganizerEmail != "" {
		attendees = append(attendees, calEventAttendee{
			Email:       p.OrganizerEmail,
			DisplayName: p.OrganizerName,
		})
	}

	body, err := json.Marshal(calEventReq{
		Summary:     p.Summary,
		Description: p.Description,
		Start:       calEventDateTime{DateTime: p.Start.UTC().Format(time.RFC3339), TimeZone: "UTC"},
		End:         calEventDateTime{DateTime: p.End.UTC().Format(time.RFC3339), TimeZone: "UTC"},
		Attendees:   attendees,
	})
	if err != nil {
		return "", fmt.Errorf("gcal: create event marshal: %w", err)
	}

	apiURL := c.apiBase + "/calendars/" + url.PathEscape(calID) + "/events?sendUpdates=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("gcal: create event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("gcal: create event call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gcal: create event status %d", resp.StatusCode)
	}

	var evResp calEventResp
	if err := json.NewDecoder(resp.Body).Decode(&evResp); err != nil {
		return "", fmt.Errorf("gcal: create event decode: %w", err)
	}
	return evResp.ID, nil
}

// CancelEvent deletes a Google Calendar event by its event ID.
// Returns nil if eventID is empty or the user has no connection.
func (c *Client) CancelEvent(ctx context.Context, userID, eventID string) error {
	if eventID == "" {
		return nil
	}
	hc, calID, err := c.DestinationClient(ctx, userID)
	if err != nil || hc == nil {
		return err
	}

	apiURL := c.apiBase + "/calendars/" + url.PathEscape(calID) + "/events/" + url.PathEscape(eventID) + "?sendUpdates=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("gcal: cancel event request: %w", err)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("gcal: cancel event call: %w", err)
	}
	defer resp.Body.Close()

	// 204 No Content = deleted; 410 Gone = already deleted — both are fine.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusGone {
		return fmt.Errorf("gcal: cancel event status %d", resp.StatusCode)
	}
	return nil
}
