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
	Name() string        // "google" | "microsoft"
	InvitesGuests() bool // provider emails guests itself → suppress our own .ics

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

// providerForDestination resolves the provider of the user's DESTINATION connection — the one
// calendar bookings are written to (is_destination = 1). Write/capability ops route here.
// Returns nil if the user has no destination.
func (s *Service) providerForDestination(ctx context.Context, userID string) Provider {
	var name string
	if err := s.db.QueryRowContext(ctx,
		`SELECT provider FROM calendar_connections WHERE user_id = ? AND is_destination = 1 LIMIT 1`, userID).Scan(&name); err != nil {
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

// CanAutoGenerate reports whether the user's connected calendar can natively
// auto-generate the meeting link for an online location type: Google Meet from any
// connected Google calendar, Microsoft Teams only from a connected work/school
// Microsoft account (personal Microsoft accounts can't mint Teams-for-Business
// links). An account_kind of "" (unknown — legacy rows) is treated as capable.
// Returns false when the user has no connection or the type isn't an online type.
func (s *Service) CanAutoGenerate(ctx context.Context, userID, locType string) (bool, error) {
	var provider, kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT provider, COALESCE(account_kind, '') FROM calendar_connections WHERE user_id = ? AND is_destination = 1 LIMIT 1`,
		userID).Scan(&provider, &kind)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch locType {
	case "google_meet":
		return provider == "google", nil
	case "teams":
		return provider == "microsoft" && kind != "personal", nil
	default:
		return false, nil
	}
}

// Disconnect removes all calendar connections for the user (any provider).
func (s *Service) Disconnect(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM calendar_connections WHERE user_id = ?`, userID)
	return err
}

// Connection is one connected calendar account for the multi-calendar UI/API.
type Connection struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	AccountEmail   string `json:"account_email"`
	IsDestination  bool   `json:"is_destination"`
	CheckConflicts bool   `json:"check_conflicts"`
}

// Connections lists the user's connected calendars (all providers), destination first.
func (s *Service) Connections(ctx context.Context, userID string) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider, COALESCE(account_email,''), is_destination, check_conflicts
		FROM calendar_connections WHERE user_id = ?
		ORDER BY is_destination DESC, created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		var c Connection
		var dest, check int
		if err := rows.Scan(&c.ID, &c.Provider, &c.AccountEmail, &dest, &check); err != nil {
			return nil, err
		}
		c.IsDestination = dest != 0
		c.CheckConflicts = check != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetDestination makes connID the user's single write destination (clears the flag on the
// rest). Errors if connID isn't one of the user's connections.
func (s *Service) SetDestination(ctx context.Context, userID, connID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var owned int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_connections WHERE id = ? AND user_id = ?`, connID, userID).Scan(&owned); err != nil {
		return err
	}
	if owned == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_connections SET is_destination = 0 WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_connections SET is_destination = 1 WHERE id = ? AND user_id = ?`, connID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DisconnectOne removes one of the user's connections. If it was the destination, the
// oldest remaining connection is promoted so the user keeps a write target.
func (s *Service) DisconnectOne(ctx context.Context, userID, connID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var wasDest int
	switch err := tx.QueryRowContext(ctx,
		`SELECT is_destination FROM calendar_connections WHERE id = ? AND user_id = ?`, connID, userID).Scan(&wasDest); err {
	case nil:
	case sql.ErrNoRows:
		return nil // already gone / not theirs — no-op
	default:
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE id = ? AND user_id = ?`, connID, userID); err != nil {
		return err
	}
	if wasDest != 0 {
		// Promote the oldest remaining connection to destination (if any).
		var nextID string
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM calendar_connections WHERE user_id = ? ORDER BY created_at ASC LIMIT 1`, userID).Scan(&nextID); err == nil {
			if _, err := tx.ExecContext(ctx,
				`UPDATE calendar_connections SET is_destination = 1 WHERE id = ?`, nextID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// HasDestination reports whether the user has a destination calendar to write events to.
// Used to gate the email .ics fallback and skip pointless retries.
func (s *Service) HasDestination(ctx context.Context, userID string) (bool, error) {
	if p := s.providerForDestination(ctx, userID); p != nil {
		return p.HasDestination(ctx, userID)
	}
	return false, nil
}

// InvitesGuests reports whether the user's DESTINATION provider emails guests itself.
func (s *Service) InvitesGuests(ctx context.Context, userID string) bool {
	if p := s.providerForDestination(ctx, userID); p != nil {
		return p.InvitesGuests()
	}
	return false
}

// FreeBusy returns the UNION of busy intervals for the user across EVERY connected provider
// (each provider internally unions its own connected accounts with check_conflicts = 1). This
// is what lets a user connect multiple calendars and have them all checked. Fail-open: a
// provider that errors is skipped; an error is returned only if every provider failed (so a
// flaky calendar never blocks availability or a booking).
func (s *Service) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	var out []slots.Interval
	var firstErr error
	anyOK := false
	for _, p := range s.providers {
		iv, err := p.FreeBusy(ctx, userID, from, to)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		anyOK = true
		out = append(out, iv...)
	}
	if !anyOK && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// CreateEvent creates an event on the user's DESTINATION calendar; returns ("","",nil) if
// they have no destination.
func (s *Service) CreateEvent(ctx context.Context, userID string, p CreateEventParams) (string, string, error) {
	if pr := s.providerForDestination(ctx, userID); pr != nil {
		return pr.CreateEvent(ctx, userID, p)
	}
	return "", "", nil
}

// UpdateEvent moves an event on the user's DESTINATION calendar.
func (s *Service) UpdateEvent(ctx context.Context, userID, eventID string, start, end time.Time) error {
	if pr := s.providerForDestination(ctx, userID); pr != nil {
		return pr.UpdateEvent(ctx, userID, eventID, start, end)
	}
	return nil
}

// CancelEvent deletes an event on the user's DESTINATION calendar.
func (s *Service) CancelEvent(ctx context.Context, userID, eventID string) error {
	if pr := s.providerForDestination(ctx, userID); pr != nil {
		return pr.CancelEvent(ctx, userID, eventID)
	}
	return nil
}
