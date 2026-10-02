package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"sort"
	"time"

	"github.com/calnode/calnode/internal/slots"
	"github.com/calnode/calnode/internal/uid"
)

var errInvitationUnavailable = errors.New("invitation is not available for scheduling")

type invitationRecipient struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type invitationExternal struct {
	System    string `json:"system"`
	Reference string `json:"reference"`
	URL       string `json:"url"`
}

// schedulingInvitation is a persisted scheduling snapshot, not a temporary
// event type. Discussion #47 and issue #92 define the defaults/overrides/result
// split. Booking redemption and delivery are deliberately later slices.
type schedulingInvitation struct {
	ID                   string              `json:"id"`
	EventTypeID          string              `json:"event_type_id"`
	CreatedBy            string              `json:"created_by"`
	Recipient            invitationRecipient `json:"recipient"`
	DurationMinutes      int                 `json:"duration_minutes"`
	SlotIntervalMinutes  int                 `json:"slot_interval_minutes"`
	BufferBeforeMinutes  int                 `json:"buffer_before_minutes"`
	BufferAfterMinutes   int                 `json:"buffer_after_minutes"`
	MinNoticeMinutes     int                 `json:"min_notice_minutes"`
	MaxFutureDays        int                 `json:"max_future_days"`
	RoutingMode          string              `json:"routing_mode"`
	RRStrategy           string              `json:"rr_strategy"`
	Hosts                []EventHost         `json:"hosts"`
	AvailableFrom        *time.Time          `json:"available_from,omitempty"`
	AvailableUntil       *time.Time          `json:"available_until,omitempty"`
	AvailabilityTimezone string              `json:"availability_timezone"`
	External             invitationExternal  `json:"external"`
	Delivery             string              `json:"delivery"`
	Status               string              `json:"status"`
	BookingID            *string             `json:"booking_id,omitempty"`
	ExpiresAt            time.Time           `json:"expires_at"`
	CreatedAt            string              `json:"created_at"`
}

// invitationWindow accepts inclusive local dates or precise RFC3339 bounds.
// Date-only upper bounds advance by a calendar day (not 24 hours), preserving
// the selected final date even across DST. Stored bounds are always UTC.
func invitationWindow(from, until, tzName string) (*time.Time, *time.Time, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, nil, errInvalidTimezone
	}
	parse := func(value string, upper bool) (*time.Time, error) {
		if value == "" {
			return nil, nil
		}
		t, err := time.ParseInLocation("2006-01-02", value, loc)
		if err == nil {
			if upper {
				t = t.AddDate(0, 0, 1)
			}
		} else {
			t, err = time.Parse(time.RFC3339Nano, value)
		}
		if err != nil || t.IsZero() {
			return nil, fmt.Errorf("availability bounds must be dates or RFC3339 timestamps")
		}
		t = t.UTC()
		return &t, nil
	}
	start, err := parse(from, false)
	if err != nil {
		return nil, nil, err
	}
	end, err := parse(until, true)
	if err != nil {
		return nil, nil, err
	}
	if start != nil && end != nil && !end.After(*start) {
		return nil, nil, fmt.Errorf("available_until must follow available_from")
	}
	return start, end, nil
}

// invitationRouting uses the existing host-role semantics. An explicit host
// restriction can turn a rotation template into a single fixed-host invitation.
func invitationRouting(hosts []EventHost) (string, error) {
	required, rotation := 0, 0
	seen := map[string]bool{}
	for _, host := range hosts {
		if host.UserID == "" || seen[host.UserID] {
			return "", fmt.Errorf("hosts need unique, non-empty user_id values")
		}
		seen[host.UserID] = true
		switch host.Role {
		case "required":
			required++
		case "rotation":
			rotation++
		case "optional":
		default:
			return "", fmt.Errorf("host role must be 'required', 'rotation', or 'optional'")
		}
	}
	if rotation > 0 {
		return "round_robin", nil
	}
	if required > 1 {
		return "collective", nil
	}
	if required == 1 {
		return "fixed", nil
	}
	return "", fmt.Errorf("an invitation needs a required or rotation host")
}

// CreateSchedulingInvitation handles POST /v1/scheduling-invitations. Like
// event-type editing, issuance is owner-scoped. Private active templates are
// allowed: authenticated staff authorize the snapshot, not a public booker.
func (h *Handler) CreateSchedulingInvitation(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		EventTypeSlug        string              `json:"event_type_slug"`
		Recipient            invitationRecipient `json:"recipient"`
		DurationMinutes      *int                `json:"duration_minutes"`
		Hosts                *[]EventHost        `json:"hosts"`
		AvailableFrom        string              `json:"available_from"`
		AvailableUntil       string              `json:"available_until"`
		AvailabilityTimezone string              `json:"availability_timezone"`
		ExpiresAt            time.Time           `json:"expires_at"`
		External             invitationExternal  `json:"external"`
		Delivery             string              `json:"delivery"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	address, err := mail.ParseAddress(req.Recipient.Email)
	if req.EventTypeSlug == "" || err != nil || address.Address != req.Recipient.Email {
		h.writeError(w, http.StatusBadRequest, "event_type_slug and a recipient email address are required")
		return
	}
	if !req.ExpiresAt.After(time.Now().UTC()) {
		h.writeError(w, http.StatusBadRequest, "expires_at must be in the future")
		return
	}
	if req.Delivery == "" {
		req.Delivery = "external"
	}
	if req.Delivery != "external" && req.Delivery != "calnode" {
		h.writeError(w, http.StatusBadRequest, "delivery must be 'external' or 'calnode'")
		return
	}
	if req.AvailabilityTimezone == "" {
		req.AvailabilityTimezone = "UTC"
	}
	from, until, err := invitationWindow(req.AvailableFrom, req.AvailableUntil, req.AvailabilityTimezone)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	et, err := scanEventType(tx.QueryRowContext(r.Context(), selectETCols+
		" WHERE slug = ? AND user_id = ? AND archived_at IS NULL", req.EventTypeSlug, user.ID))
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	if et == nil || !et.IsActive {
		h.writeError(w, http.StatusNotFound, "active event type not found")
		return
	}
	duration := et.DurationMinutes
	if req.DurationMinutes != nil {
		duration = *req.DurationMinutes
	}
	if err := (durationPolicy{et.MinDurationMinutes, et.MaxDurationMinutes, et.DurationIncrementMinutes}).allows(duration, et.DurationMinutes); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Fully materialize before any further query or write: SQLite has one connection.
	rows, err := tx.QueryContext(r.Context(), `SELECT eth.user_id, eth.role, eth.priority
		FROM event_type_hosts eth JOIN users u ON u.id = eth.user_id
		WHERE eth.event_type_id = ? AND u.archived_at IS NULL
		ORDER BY eth.priority, eth.user_id`, et.ID)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	hosts := []EventHost{}
	for rows.Next() {
		var host EventHost
		if err := rows.Scan(&host.UserID, &host.Role, &host.Priority); err != nil {
			rows.Close() //nolint:errcheck
			h.invitationError(w, r, err)
			return
		}
		hosts = append(hosts, host)
	}
	err = rows.Err()
	rows.Close() //nolint:errcheck
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	routing := et.RoutingMode
	if req.Hosts != nil {
		eligible := map[string]bool{}
		for _, host := range hosts {
			eligible[host.UserID] = true
		}
		hosts = *req.Hosts
		for _, host := range hosts {
			if !eligible[host.UserID] {
				h.writeError(w, http.StatusBadRequest, "invitation hosts must be active event-type hosts")
				return
			}
		}
		routing, err = invitationRouting(hosts)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateInvitationHosts(hosts, routing); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].Priority != hosts[j].Priority {
			return hosts[i].Priority < hosts[j].Priority
		}
		return hosts[i].UserID < hosts[j].UserID
	})
	id := uid.New()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO scheduling_invitations
		(id, event_type_id, created_by, recipient_name, recipient_email,
		duration_minutes, slot_interval_minutes, buffer_before_minutes, buffer_after_minutes,
		min_notice_minutes, max_future_days, routing_mode, rr_strategy,
		available_from, available_until, availability_timezone, external_system, external_reference,
		external_url, delivery, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, et.ID, user.ID, req.Recipient.Name, req.Recipient.Email, duration, et.SlotIntervalMinutes,
		et.BufferBeforeMinutes, et.BufferAfterMinutes, et.MinNoticeMinutes, et.MaxFutureDays,
		routing, et.RRStrategy, invitationTimeString(from), invitationTimeString(until),
		req.AvailabilityTimezone, req.External.System, req.External.Reference, req.External.URL,
		req.Delivery, req.ExpiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	for _, host := range hosts {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO scheduling_invitation_hosts
			(invitation_id, user_id, role, priority) VALUES (?, ?, ?, ?)`, id, host.UserID, host.Role, host.Priority); err != nil {
			h.invitationError(w, r, err)
			return
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.invitationError(w, r, err)
		return
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO scheduling_invitation_tokens
		(token_hash, invitation_id, expires_at) VALUES (?, ?, ?)`,
		hex.EncodeToString(sum[:]), id, req.ExpiresAt.UTC().Format(time.RFC3339Nano)); err != nil {
		h.invitationError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.invitationError(w, r, err)
		return
	}
	inv, err := h.loadSchedulingInvitation(r.Context(), id, user.ID)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	// Returned once; reads never expose the bearer credential. No scheduling URL
	// is advertised until the public redemption flow enforces all restrictions.
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusCreated, struct {
		*schedulingInvitation
		Token string `json:"token"`
	}{inv, token})
}

func invitationTimeString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func validateInvitationHosts(hosts []EventHost, mode string) error {
	derived, err := invitationRouting(hosts)
	if err != nil {
		return err
	}
	if mode == "round_robin" && derived != "round_robin" ||
		mode != "round_robin" && derived == "round_robin" ||
		mode == "fixed" && derived != "fixed" {
		return fmt.Errorf("host roles do not satisfy the invitation routing mode")
	}
	switch mode {
	case "fixed", "round_robin", "collective", "priority":
		return nil
	default:
		return fmt.Errorf("invalid invitation routing mode")
	}
}

func (h *Handler) loadSchedulingInvitation(ctx context.Context, id, creator string) (*schedulingInvitation, error) {
	var inv schedulingInvitation
	var from, until sql.NullString
	var expires string
	err := h.db.QueryRowContext(ctx, `SELECT id, event_type_id, created_by, recipient_name, recipient_email,
		duration_minutes, slot_interval_minutes, buffer_before_minutes, buffer_after_minutes,
		min_notice_minutes, max_future_days, routing_mode, rr_strategy, available_from, available_until,
		availability_timezone, external_system, external_reference, external_url, delivery, status,
		booking_id, expires_at, created_at FROM scheduling_invitations WHERE id = ? AND created_by = ?`, id, creator).
		Scan(&inv.ID, &inv.EventTypeID, &inv.CreatedBy, &inv.Recipient.Name, &inv.Recipient.Email,
			&inv.DurationMinutes, &inv.SlotIntervalMinutes, &inv.BufferBeforeMinutes, &inv.BufferAfterMinutes,
			&inv.MinNoticeMinutes, &inv.MaxFutureDays, &inv.RoutingMode, &inv.RRStrategy, &from, &until,
			&inv.AvailabilityTimezone, &inv.External.System, &inv.External.Reference, &inv.External.URL,
			&inv.Delivery, &inv.Status, &inv.BookingID, &expires, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	if inv.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires); err != nil {
		return nil, errInvitationUnavailable
	}
	for _, bound := range []struct {
		raw  sql.NullString
		dest **time.Time
	}{{from, &inv.AvailableFrom}, {until, &inv.AvailableUntil}} {
		if !bound.raw.Valid {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, bound.raw.String)
		if err != nil || t.IsZero() {
			return nil, errInvitationUnavailable
		}
		*bound.dest = &t
	}
	rows, err := h.db.QueryContext(ctx, `SELECT user_id, role, priority FROM scheduling_invitation_hosts
		WHERE invitation_id = ? ORDER BY priority, user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	inv.Hosts = []EventHost{}
	for rows.Next() {
		var host EventHost
		if err := rows.Scan(&host.UserID, &host.Role, &host.Priority); err != nil {
			return nil, err
		}
		inv.Hosts = append(inv.Hosts, host)
	}
	return &inv, rows.Err()
}

func (h *Handler) invitationSchedulingContext(ctx context.Context, inv *schedulingInvitation) (schedulingContext, error) {
	if inv.Status != "active" || !inv.ExpiresAt.After(time.Now().UTC()) || inv.BookingID != nil ||
		inv.DurationMinutes <= 0 || inv.SlotIntervalMinutes <= 0 || inv.Recipient.Email == "" ||
		inv.BufferBeforeMinutes < 0 || inv.BufferAfterMinutes < 0 || inv.MinNoticeMinutes < 0 || inv.MaxFutureDays < 0 {
		return schedulingContext{}, errInvitationUnavailable
	}
	var tokenExpires string
	var consumed sql.NullString
	if err := h.db.QueryRowContext(ctx, `SELECT expires_at, consumed_at FROM scheduling_invitation_tokens WHERE invitation_id = ?`, inv.ID).
		Scan(&tokenExpires, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingContext{}, errInvitationUnavailable
		}
		return schedulingContext{}, err
	}
	expires, err := time.Parse(time.RFC3339Nano, tokenExpires)
	if err != nil || consumed.Valid || !expires.After(time.Now().UTC()) {
		return schedulingContext{}, errInvitationUnavailable
	}
	if err := validateInvitationHosts(inv.Hosts, inv.RoutingMode); err != nil {
		return schedulingContext{}, errInvitationUnavailable
	}
	var active int
	if err := h.db.QueryRowContext(ctx, `SELECT is_active FROM event_types WHERE id = ? AND archived_at IS NULL`, inv.EventTypeID).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schedulingContext{}, errInvitationUnavailable
		}
		return schedulingContext{}, err
	}
	if active == 0 {
		return schedulingContext{}, errInvitationUnavailable
	}
	// Do not rejoin event_type_hosts: changing a template must not change a
	// previously authorized invitation. Required hosts cannot silently disappear.
	hosts := []EventHost{}
	for _, host := range inv.Hosts {
		var archived sql.NullString
		if err := h.db.QueryRowContext(ctx, `SELECT archived_at FROM users WHERE id = ?`, host.UserID).Scan(&archived); err != nil {
			return schedulingContext{}, err
		}
		if archived.Valid {
			if host.Role == "required" {
				return schedulingContext{}, errInvitationUnavailable
			}
			continue
		}
		hosts = append(hosts, host)
	}
	schedule := schedulingContext{EventTypeID: inv.EventTypeID, Hosts: hosts, Event: slots.EventConfig{
		DurationMinutes: inv.DurationMinutes, SlotIntervalMinutes: inv.SlotIntervalMinutes,
		BufferBeforeMinutes: inv.BufferBeforeMinutes, BufferAfterMinutes: inv.BufferAfterMinutes,
		MinNoticeMinutes: inv.MinNoticeMinutes, MaxFutureDays: inv.MaxFutureDays, RoutingMode: inv.RoutingMode,
	}}
	if inv.AvailableFrom != nil || inv.AvailableUntil != nil {
		window := &slots.Window{}
		if inv.AvailableFrom != nil {
			window.Start = *inv.AvailableFrom
		}
		if inv.AvailableUntil != nil {
			window.End = *inv.AvailableUntil
		}
		if err := window.Validate(); err != nil {
			return schedulingContext{}, errInvitationUnavailable
		}
		schedule.AllowedWindow = window
	}
	return schedule, nil
}

func (h *Handler) invitationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		h.writeError(w, http.StatusNotFound, "invitation not found")
	case errors.Is(err, errInvitationUnavailable):
		h.writeError(w, http.StatusConflict, errInvitationUnavailable.Error())
	case errors.Is(err, errInvalidTimezone), errors.Is(err, errBadDateRange):
		h.writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.ErrorContext(r.Context(), "scheduling invitation", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// GetSchedulingInvitation handles GET /v1/scheduling-invitations/{id}.
func (h *Handler) GetSchedulingInvitation(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	inv, err := h.loadSchedulingInvitation(r.Context(), r.PathValue("id"), user.ID)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusOK, inv)
}

// GetSchedulingInvitationSlots is an authenticated preview. Public token
// endpoints must wait for the atomic booking/claim flow in a subsequent slice.
func (h *Handler) GetSchedulingInvitationSlots(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	inv, err := h.loadSchedulingInvitation(r.Context(), r.PathValue("id"), user.ID)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	schedule, err := h.invitationSchedulingContext(r.Context(), inv)
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	q := r.URL.Query()
	res, err := h.computeSlotsForContext(r.Context(), schedule, q.Get("timezone"), q.Get("from"), q.Get("to"), slotsWanted{})
	if err != nil {
		h.invitationError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusOK, map[string]any{"slots": res.Slots, "hosts": res.Hosts, "degraded": res.Degraded})
}
