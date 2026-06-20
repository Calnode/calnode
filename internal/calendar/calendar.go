// Package calendar abstracts external calendar providers (Google, Microsoft, …)
// behind a single Provider interface and a Service that routes per-user by the
// provider stored on their calendar_connections row. The booking/slot/reconciler
// code talks only to *Service and never to a concrete provider.
package calendar

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

// CreateEventParams holds the data needed to create a calendar event. Provider-
// agnostic: AddMeet requests the provider's online-meeting (Google Meet / Teams).
type CreateEventParams struct {
	Summary        string
	Description    string
	Location       string // optional; e.g. the meeting link on secondary hosts' events
	Start, End     time.Time
	OrganizerName  string
	OrganizerEmail string
	AddMeet        bool
}

// Provider is one calendar backend for a connected user. All operations are keyed
// by userID and resolve that user's stored credentials internally; they return
// zero values (not errors) when the user has no matching connection.
type Provider interface {
	Name() string         // "google" | "microsoft"
	InvitesGuests() bool  // provider emails guests itself → suppress our own .ics

	// OAuth
	AuthURL(state string) string
	EncryptState(userID string) (string, error)
	DecryptState(state string) (string, error)
	Exchange(ctx context.Context, userID, code, calendarID string) error

	// Connection state
	Connected(ctx context.Context, userID string) (bool, error)
	Disconnect(ctx context.Context, userID string) error
	HasDestination(ctx context.Context, userID string) (bool, error)

	// Operations
	FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error)
	CreateEvent(ctx context.Context, userID string, p CreateEventParams) (eventID, joinURL string, err error)
	UpdateEvent(ctx context.Context, userID, eventID string, start, end time.Time) error
	CancelEvent(ctx context.Context, userID, eventID string) error
}

// Service holds the configured providers and dispatches per-user operations to
// whichever provider that user has connected.
type Service struct {
	db        *sql.DB
	providers map[string]Provider
	primary   string // default provider for new connections (first registered)
}

// NewService returns an empty Service. Register one provider per configured backend.
func NewService(db *sql.DB) *Service {
	return &Service{db: db, providers: map[string]Provider{}}
}

// Register adds a provider (keyed by Name()); the first registered becomes primary.
func (s *Service) Register(p Provider) {
	s.providers[p.Name()] = p
	if s.primary == "" {
		s.primary = p.Name()
	}
}

// Any reports whether at least one provider is configured.
func (s *Service) Any() bool { return s != nil && len(s.providers) > 0 }

// Primary returns the default provider for new connections (nil if none).
func (s *Service) Primary() Provider { return s.providers[s.primary] }

// Provider returns the named provider, or nil.
func (s *Service) Provider(name string) Provider { return s.providers[name] }

// ProviderNames returns the configured provider names, sorted, so the UI can
// offer the right connect options.
func (s *Service) ProviderNames() []string {
	names := make([]string, 0, len(s.providers))
	for n := range s.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// providerForUser resolves the provider a user has connected (nil if none).
func (s *Service) providerForUser(ctx context.Context, userID string) Provider {
	var name string
	if err := s.db.QueryRowContext(ctx,
		`SELECT provider FROM calendar_connections WHERE user_id = ? LIMIT 1`, userID).Scan(&name); err != nil {
		return nil
	}
	return s.providers[name]
}

// Connected reports whether the user has any calendar connection, and which provider.
func (s *Service) Connected(ctx context.Context, userID string) (bool, string, error) {
	var name string
	err := s.db.QueryRowContext(ctx,
		`SELECT provider FROM calendar_connections WHERE user_id = ? LIMIT 1`, userID).Scan(&name)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, name, nil
}

// Disconnect removes all calendar connections for the user (any provider).
func (s *Service) Disconnect(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM calendar_connections WHERE user_id = ?`, userID)
	return err
}

// RetainOnly removes the user's connections for every provider except keep —
// enforcing one connected calendar per user when they switch providers (so
// connecting Microsoft replaces a prior Google connection, and vice versa).
func (s *Service) RetainOnly(ctx context.Context, userID, keep string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider != ?`, userID, keep)
	return err
}

// HasDestination reports whether the user's connected provider has somewhere to
// write events. Used to gate the email .ics fallback and skip pointless retries.
func (s *Service) HasDestination(ctx context.Context, userID string) (bool, error) {
	if p := s.providerForUser(ctx, userID); p != nil {
		return p.HasDestination(ctx, userID)
	}
	return false, nil
}

// InvitesGuests reports whether the user's connected provider emails guests itself.
func (s *Service) InvitesGuests(ctx context.Context, userID string) bool {
	if p := s.providerForUser(ctx, userID); p != nil {
		return p.InvitesGuests()
	}
	return false
}

// FreeBusy returns busy intervals for the user from their connected provider.
func (s *Service) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	if p := s.providerForUser(ctx, userID); p != nil {
		return p.FreeBusy(ctx, userID, from, to)
	}
	return nil, nil
}

// CreateEvent creates an event on the user's connected provider; returns ("","",nil)
// if they have none.
func (s *Service) CreateEvent(ctx context.Context, userID string, p CreateEventParams) (string, string, error) {
	if pr := s.providerForUser(ctx, userID); pr != nil {
		return pr.CreateEvent(ctx, userID, p)
	}
	return "", "", nil
}

// UpdateEvent moves an event on the user's connected provider.
func (s *Service) UpdateEvent(ctx context.Context, userID, eventID string, start, end time.Time) error {
	if pr := s.providerForUser(ctx, userID); pr != nil {
		return pr.UpdateEvent(ctx, userID, eventID, start, end)
	}
	return nil
}

// CancelEvent deletes an event on the user's connected provider.
func (s *Service) CancelEvent(ctx context.Context, userID, eventID string) error {
	if pr := s.providerForUser(ctx, userID); pr != nil {
		return pr.CancelEvent(ctx, userID, eventID)
	}
	return nil
}
