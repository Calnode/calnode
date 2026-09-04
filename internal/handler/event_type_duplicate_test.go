package handler_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// duplicateResponse is the subset of the created copy the tests assert on.
type duplicateResponse struct {
	ID                  string  `json:"id"`
	Slug                string  `json:"slug"`
	Name                string  `json:"name"`
	Description         *string `json:"description"`
	DurationMinutes     int     `json:"duration_minutes"`
	SlotIntervalMinutes int     `json:"slot_interval_minutes"`
	LocationType        string  `json:"location_type"`
	LocationValue       *string `json:"location_value"`
	RoutingMode         string  `json:"routing_mode"`
	RRStrategy          string  `json:"rr_strategy"`
	BufferBeforeMinutes int     `json:"buffer_before_minutes"`
	BufferAfterMinutes  int     `json:"buffer_after_minutes"`
	MinNoticeMinutes    int     `json:"min_notice_minutes"`
	MaxFutureDays       int     `json:"max_future_days"`
	MaxActiveBookings   int     `json:"max_active_bookings"`
	IsActive            bool    `json:"is_active"`
	IsPublic            bool    `json:"is_public"`
	ShowTakenSlots      bool    `json:"show_taken_slots"`
	Archived            bool    `json:"archived"`
	MsgConfirmation     *string `json:"msg_confirmation"`
	SubjConfirmation    *string `json:"subj_confirmation"`
	MsgGreeting         *string `json:"msg_greeting"`
	PriceCents          int     `json:"price_cents"`
	Currency            string  `json:"currency"`
	Reminders           []int   `json:"reminders"`
}

// duplicate POSTs /v1/event-types/{slug}/duplicate and returns the recorder.
func duplicate(t *testing.T, h *handler.Handler, apiKey, slug string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/duplicate", "", apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DuplicateEventType)(rec, req)
	return rec
}

// seedRichEventType inserts an event type with a non-default value in every column that
// a copy has to carry, plus one row in each child table. Direct SQL rather than the API
// so columns the editor doesn't expose (seat_limit, team_id) are covered too.
func seedRichEventType(t *testing.T, database *sql.DB, ownerID, memberID string) {
	t.Helper()
	mustExec(t, database, `
		INSERT INTO event_types (
		  id, user_id, slug, name, description,
		  duration_minutes, slot_interval_minutes, location_type, location_value,
		  routing_mode, rr_strategy, buffer_before_minutes, buffer_after_minutes,
		  min_notice_minutes, max_future_days, max_active_bookings, seat_limit,
		  is_active, is_public, show_taken_slots,
		  msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, msg_greeting,
		  subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder,
		  price_cents, currency)
		VALUES (
		  'src', ?, 'intro-call', 'Intro Call', 'A chat about the role',
		  45, 15, 'phone', '+15550100',
		  'round_robin', 'priority', 10, 20,
		  240, 45, 3, 4,
		  1, 0, 1,
		  'See you soon', 'Sorry to miss you', 'New time below', 'Coming up',
		  'Hi! When suits you?',
		  'Confirmed: intro', 'Cancelled: intro', 'Moved: intro', 'Reminder: intro',
		  5000, 'eur')`, ownerID)

	// Intake form: a required free-text question and an optional select with options.
	mustExec(t, database, `
		INSERT INTO event_type_questions (id, event_type_id, label, type, options, required, position)
		VALUES ('q1', 'src', 'What would you like to cover?', 'text', NULL, 1, 0)`)
	mustExec(t, database, `
		INSERT INTO event_type_questions (id, event_type_id, label, type, options, required, position)
		VALUES ('q2', 'src', 'How did you hear about us?', 'select', '["Search","A friend"]', 0, 1)`)

	// Host list: the owner always attends, the member is in the rotation.
	mustExec(t, database, `
		INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
		VALUES ('h1', 'src', ?, 'required', 0)`, ownerID)
	mustExec(t, database, `
		INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
		VALUES ('h2', 'src', ?, 'rotation', 1)`, memberID)

	// Two availability rules: one specific to this event type, one global default. Only
	// the first belongs in the copy.
	mustExec(t, database, `
		INSERT INTO availability_rules (id, user_id, event_type_id, day_of_week, start_time, end_time)
		VALUES ('ar1', ?, 'src', 1, '09:00', '12:00')`, ownerID)
	mustExec(t, database, `
		INSERT INTO availability_rules (id, user_id, event_type_id, day_of_week, start_time, end_time)
		VALUES ('ar2', ?, NULL, 2, '13:00', '17:00')`, ownerID)

	mustExec(t, database, `INSERT INTO event_type_reminders (id, event_type_id, hours_before) VALUES ('r1', 'src', 24)`)
	mustExec(t, database, `INSERT INTO event_type_reminders (id, event_type_id, hours_before) VALUES ('r2', 'src', 2)`)

	// A booking on the source, to prove history is not copied.
	mustExec(t, database, `
		INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		VALUES ('b1', 'src', ?, '2099-01-01T10:00:00Z', '2099-01-01T10:45:00Z', 'confirmed')`, ownerID)
}

func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("seed: %v\n  query: %s", err, query)
	}
}

// seedMember inserts a second, active workspace member.
func seedMember(t *testing.T, database *sql.DB, id, email string) string {
	t.Helper()
	mustExec(t, database,
		`INSERT INTO users (id, email, name, iana_timezone, is_admin) VALUES (?, ?, 'Member', 'UTC', 0)`, id, email)
	return id
}

func TestDuplicateEventType_copiesTheRowAndEveryChildDataset(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	memberID := seedMember(t, database, "u2", "member@example.com")
	seedRichEventType(t, database, ownerID, memberID)

	rec := duplicate(t, h, ownerKey, "intro-call")
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var got duplicateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// ── the row itself ────────────────────────────────────────────────────────
	if got.ID == "src" {
		t.Error("the copy reused the source id")
	}
	if got.Slug != "intro-call-copy" {
		t.Errorf("slug: got %q; want %q", got.Slug, "intro-call-copy")
	}
	if got.IsActive {
		t.Error("a copy must be created inactive so it cannot be published by accident")
	}
	if got.Archived {
		t.Error("a copy must not be archived")
	}
	// price_cents/currency faithfully, NOT zeroed: an operator who publishes a copy
	// believing it kept its price must not start selling the meeting for nothing.
	if got.PriceCents != 5000 || got.Currency != "eur" {
		t.Errorf("price: got %d %q; want 5000 \"eur\"", got.PriceCents, got.Currency)
	}
	if got.Name != "Intro Call" {
		t.Errorf("name: got %q; want %q", got.Name, "Intro Call")
	}
	if got.Description == nil || *got.Description != "A chat about the role" {
		t.Errorf("description: got %v", got.Description)
	}
	if got.DurationMinutes != 45 || got.SlotIntervalMinutes != 15 {
		t.Errorf("duration/interval: got %d/%d; want 45/15", got.DurationMinutes, got.SlotIntervalMinutes)
	}
	if got.LocationType != "phone" || got.LocationValue == nil || *got.LocationValue != "+15550100" {
		t.Errorf("location: got %q/%v", got.LocationType, got.LocationValue)
	}
	if got.RoutingMode != "round_robin" || got.RRStrategy != "priority" {
		t.Errorf("routing: got %q/%q; want round_robin/priority", got.RoutingMode, got.RRStrategy)
	}
	if got.BufferBeforeMinutes != 10 || got.BufferAfterMinutes != 20 {
		t.Errorf("buffers: got %d/%d; want 10/20", got.BufferBeforeMinutes, got.BufferAfterMinutes)
	}
	if got.MinNoticeMinutes != 240 || got.MaxFutureDays != 45 || got.MaxActiveBookings != 3 {
		t.Errorf("limits: got %d/%d/%d; want 240/45/3",
			got.MinNoticeMinutes, got.MaxFutureDays, got.MaxActiveBookings)
	}
	if got.IsPublic {
		t.Error("is_public: got true; want the source's false")
	}
	if !got.ShowTakenSlots {
		t.Error("show_taken_slots: got false; want the source's true")
	}
	if got.MsgConfirmation == nil || *got.MsgConfirmation != "See you soon" {
		t.Errorf("msg_confirmation: got %v", got.MsgConfirmation)
	}
	if got.SubjConfirmation == nil || *got.SubjConfirmation != "Confirmed: intro" {
		t.Errorf("subj_confirmation: got %v", got.SubjConfirmation)
	}
	if got.MsgGreeting == nil || *got.MsgGreeting != "Hi! When suits you?" {
		t.Errorf("msg_greeting: got %v", got.MsgGreeting)
	}

	// seat_limit isn't in the API shape; check it on the row.
	var seatLimit int
	var createdAt string
	if err := database.QueryRow(
		`SELECT seat_limit, created_at FROM event_types WHERE id = ?`, got.ID).
		Scan(&seatLimit, &createdAt); err != nil {
		t.Fatalf("read copy row: %v", err)
	}
	if seatLimit != 4 {
		t.Errorf("seat_limit: got %d; want 4", seatLimit)
	}
	if createdAt == "" {
		t.Error("the copy has no created_at")
	}

	// ── intake questions ──────────────────────────────────────────────────────
	type qRow struct {
		id, label, qType string
		options          sql.NullString
		required, pos    int
	}
	var questions []qRow
	rows, err := database.Query(`
		SELECT id, label, type, options, required, position
		FROM event_type_questions WHERE event_type_id = ? ORDER BY position`, got.ID)
	if err != nil {
		t.Fatalf("read copied questions: %v", err)
	}
	for rows.Next() {
		var q qRow
		if err := rows.Scan(&q.id, &q.label, &q.qType, &q.options, &q.required, &q.pos); err != nil {
			t.Fatalf("scan question: %v", err)
		}
		questions = append(questions, q)
	}
	rows.Close()
	if len(questions) != 2 {
		t.Fatalf("copied questions: got %d; want 2", len(questions))
	}
	if questions[0].label != "What would you like to cover?" || questions[0].qType != "text" ||
		questions[0].required != 1 || questions[0].pos != 0 {
		t.Errorf("question 1 not copied faithfully: %+v", questions[0])
	}
	if questions[0].options.Valid {
		t.Errorf("question 1 options: got %q; want NULL", questions[0].options.String)
	}
	if questions[1].label != "How did you hear about us?" || questions[1].qType != "select" ||
		questions[1].options.String != `["Search","A friend"]` || questions[1].required != 0 || questions[1].pos != 1 {
		t.Errorf("question 2 not copied faithfully: %+v", questions[1])
	}
	for _, q := range questions {
		if q.id == "q1" || q.id == "q2" {
			t.Errorf("copied question reused the source id %q — the rows would be shared, not copied", q.id)
		}
	}

	// ── host assignments ──────────────────────────────────────────────────────
	type hRow struct {
		id, userID, role string
		priority         int
	}
	var hosts []hRow
	rows, err = database.Query(`
		SELECT id, user_id, role, priority FROM event_type_hosts
		WHERE event_type_id = ? ORDER BY priority`, got.ID)
	if err != nil {
		t.Fatalf("read copied hosts: %v", err)
	}
	for rows.Next() {
		var hr hRow
		if err := rows.Scan(&hr.id, &hr.userID, &hr.role, &hr.priority); err != nil {
			t.Fatalf("scan host: %v", err)
		}
		hosts = append(hosts, hr)
	}
	rows.Close()
	if len(hosts) != 2 {
		t.Fatalf("copied hosts: got %d; want 2", len(hosts))
	}
	if hosts[0].userID != ownerID || hosts[0].role != "required" || hosts[0].priority != 0 {
		t.Errorf("host 1 not copied faithfully: %+v", hosts[0])
	}
	if hosts[1].userID != memberID || hosts[1].role != "rotation" || hosts[1].priority != 1 {
		t.Errorf("host 2 not copied faithfully: %+v", hosts[1])
	}
	for _, hr := range hosts {
		if hr.id == "h1" || hr.id == "h2" {
			t.Errorf("copied host reused the source id %q", hr.id)
		}
	}

	// ── availability rules ────────────────────────────────────────────────────
	var ruleCount int
	var ruleUser, ruleStart, ruleEnd string
	var ruleDOW int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM availability_rules WHERE event_type_id = ?`, got.ID).Scan(&ruleCount); err != nil {
		t.Fatalf("count copied rules: %v", err)
	}
	if ruleCount != 1 {
		t.Fatalf("copied availability rules: got %d; want 1 (the event-specific rule only)", ruleCount)
	}
	if err := database.QueryRow(`
		SELECT user_id, day_of_week, start_time, end_time FROM availability_rules
		WHERE event_type_id = ?`, got.ID).Scan(&ruleUser, &ruleDOW, &ruleStart, &ruleEnd); err != nil {
		t.Fatalf("read copied rule: %v", err)
	}
	if ruleUser != ownerID || ruleDOW != 1 || ruleStart != "09:00" || ruleEnd != "12:00" {
		t.Errorf("availability rule not copied faithfully: %s day=%d %s-%s", ruleUser, ruleDOW, ruleStart, ruleEnd)
	}
	// The host's global rule must still be exactly one global rule: it already applies
	// to the copy, so promoting it to an event-specific row would be a behaviour change.
	var globalRules int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM availability_rules WHERE event_type_id IS NULL`).Scan(&globalRules); err != nil {
		t.Fatalf("count global rules: %v", err)
	}
	if globalRules != 1 {
		t.Errorf("global availability rules: got %d; want 1 — a global rule must not be copied", globalRules)
	}

	// ── reminders ─────────────────────────────────────────────────────────────
	sort.Ints(got.Reminders)
	if len(got.Reminders) != 2 || got.Reminders[0] != 2 || got.Reminders[1] != 24 {
		t.Errorf("reminders in the response: got %v; want [2 24]", got.Reminders)
	}
	var reminderCount int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM event_type_reminders WHERE event_type_id = ?`, got.ID).Scan(&reminderCount); err != nil {
		t.Fatalf("count copied reminders: %v", err)
	}
	if reminderCount != 2 {
		t.Errorf("copied reminders: got %d; want 2", reminderCount)
	}

	// ── bookings are NOT copied ───────────────────────────────────────────────
	var bookings int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM bookings WHERE event_type_id = ?`, got.ID).Scan(&bookings); err != nil {
		t.Fatalf("count copied bookings: %v", err)
	}
	if bookings != 0 {
		t.Errorf("bookings copied onto the duplicate: got %d; want 0", bookings)
	}
}

// TestDuplicateEventType_slugIsUniqueAcrossTheInstance covers both halves of the slug
// rule: a second copy doesn't collide with the first, and a slug already taken by
// ANOTHER user's event type is still taken (event_types.slug is globally unique).
func TestDuplicateEventType_slugIsUniqueAcrossTheInstance(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	memberID := seedMember(t, database, "u2", "member@example.com")
	mustExec(t, database, `
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		VALUES ('src', ?, 'intro-call', 'Intro Call', 30)`, ownerID)
	// Another user already owns "intro-call-copy".
	mustExec(t, database, `
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		VALUES ('other', ?, 'intro-call-copy', 'Squatter', 30)`, memberID)

	rec := duplicate(t, h, ownerKey, "intro-call")
	if rec.Code != http.StatusCreated {
		t.Fatalf("first duplicate: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var first duplicateResponse
	json.Unmarshal(rec.Body.Bytes(), &first) //nolint:errcheck
	if first.Slug != "intro-call-copy-2" {
		t.Errorf("slug: got %q; want %q (\"-copy\" is taken by another user)", first.Slug, "intro-call-copy-2")
	}

	rec = duplicate(t, h, ownerKey, "intro-call")
	if rec.Code != http.StatusCreated {
		t.Fatalf("second duplicate: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var second duplicateResponse
	json.Unmarshal(rec.Body.Bytes(), &second) //nolint:errcheck
	if second.Slug != "intro-call-copy-3" {
		t.Errorf("slug: got %q; want %q", second.Slug, "intro-call-copy-3")
	}
}

// TestDuplicateEventType_archivedSourceProducesALiveDraft — archiving describes the
// original's lifecycle, so the copy starts un-archived (and, as always, inactive).
func TestDuplicateEventType_archivedSourceProducesALiveDraft(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	mustExec(t, database, `
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes, is_active, archived_at)
		VALUES ('src', ?, 'intro-call', 'Intro Call', 30, 0, '2026-01-01T00:00:00Z')`, ownerID)

	rec := duplicate(t, h, ownerKey, "intro-call")
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var got duplicateResponse
	json.Unmarshal(rec.Body.Bytes(), &got) //nolint:errcheck

	var archivedAt sql.NullString
	var isActive int
	if err := database.QueryRow(
		`SELECT archived_at, is_active FROM event_types WHERE id = ?`, got.ID).Scan(&archivedAt, &isActive); err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if archivedAt.Valid {
		t.Errorf("archived_at: got %q; want NULL", archivedAt.String)
	}
	if isActive != 0 {
		t.Error("the copy must still be inactive")
	}
}

func TestDuplicateEventType_onlyTheOwnerMayDuplicate(t *testing.T) {
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	hostKey := "assigned-host-key"
	memberID := seedMember(t, database, "u2", "member@example.com")
	mustExec(t, database,
		`INSERT INTO api_keys (id, user_id, name, key_hash, created_at) VALUES ('k2', ?, 't', ?, '2024-01-01')`,
		memberID, sha256HexForTest(hostKey))
	mustExec(t, database, `
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		VALUES ('src', ?, 'intro-call', 'Intro Call', 30)`, ownerID)
	// u2 is an assigned host on the owner's event type — visible, but read-only.
	mustExec(t, database, `
		INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
		VALUES ('h2', 'src', ?, 'rotation', 1)`, memberID)

	rec := duplicate(t, h, hostKey, "intro-call")
	if rec.Code != http.StatusNotFound {
		t.Errorf("assigned host duplicating: got %d; want 404 — %s", rec.Code, rec.Body.String())
	}
	var copies int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM event_types WHERE slug LIKE 'intro-call-copy%'`).Scan(&copies); err != nil {
		t.Fatalf("count copies: %v", err)
	}
	if copies != 0 {
		t.Errorf("copies created by a non-owner: got %d; want 0", copies)
	}
}

func TestDuplicateEventType_unknownSlugIs404(t *testing.T) {
	h, _, ownerKey, _ := setupWorkspaceWithDB(t)
	rec := duplicate(t, h, ownerKey, "does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d; want 404 — %s", rec.Code, rec.Body.String())
	}
}

// TestDuplicateEventType_rollsBackWhenAChildCopyFails proves the copy is one
// transaction: with the child insert forced to fail, no half-built event type is left
// behind. A partial copy would be worse than no copy — it looks like a real event type
// and is missing exactly the parts nobody thinks to check.
func TestDuplicateEventType_rollsBackWhenAChildCopyFails(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	memberID := seedMember(t, database, "u2", "member@example.com")
	seedRichEventType(t, database, ownerID, memberID)

	// Fail the question copy at the database, so the handler takes the same path a real
	// constraint violation would.
	mustExec(t, database, `
		CREATE TRIGGER fail_question_copy BEFORE INSERT ON event_type_questions
		BEGIN SELECT RAISE(ABORT, 'forced failure'); END`)

	rec := duplicate(t, h, ownerKey, "intro-call")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("duplicate with a failing child insert: got %d; want 500 — %s", rec.Code, rec.Body.String())
	}
	for _, q := range []struct{ what, query string }{
		{"event type", `SELECT COUNT(*) FROM event_types WHERE slug LIKE 'intro-call-copy%'`},
		{"hosts", `SELECT COUNT(*) FROM event_type_hosts WHERE event_type_id != 'src'`},
		{"availability rules", `SELECT COUNT(*) FROM availability_rules WHERE event_type_id IS NOT NULL AND event_type_id != 'src'`},
		{"reminders", `SELECT COUNT(*) FROM event_type_reminders WHERE event_type_id != 'src'`},
	} {
		var n int
		if err := database.QueryRow(q.query).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.what, err)
		}
		if n != 0 {
			t.Errorf("%s left behind after the failed copy: got %d; want 0", q.what, n)
		}
	}
}

// TestDuplicateEventType_copiesEveryEmailTemplateColumn covers the child dataset the issue
// names as "email/reminder templates". They are columns on event_types rather than a table
// of their own, so this checks all nine on the copied row rather than sampling the ones
// that happen to be in the API shape.
func TestDuplicateEventType_copiesEveryEmailTemplateColumn(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	memberID := seedMember(t, database, "u2", "member@example.com")
	seedRichEventType(t, database, ownerID, memberID)

	rec := duplicate(t, h, ownerKey, "intro-call")
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var got duplicateResponse
	json.Unmarshal(rec.Body.Bytes(), &got) //nolint:errcheck

	columns := []string{
		"msg_confirmation", "msg_cancellation", "msg_reschedule", "msg_reminder", "msg_greeting",
		"subj_confirmation", "subj_cancellation", "subj_reschedule", "subj_reminder",
	}
	for _, col := range columns {
		var src, copied sql.NullString
		// #nosec G202 -- col comes from the literal list above, never from input.
		if err := database.QueryRow(`SELECT ` + col + ` FROM event_types WHERE id = 'src'`).Scan(&src); err != nil {
			t.Fatalf("read source %s: %v", col, err)
		}
		// #nosec G202 -- same literal list.
		if err := database.QueryRow(`SELECT `+col+` FROM event_types WHERE id = ?`, got.ID).Scan(&copied); err != nil {
			t.Fatalf("read copied %s: %v", col, err)
		}
		if !src.Valid {
			t.Fatalf("fixture bug: source %s is NULL, so this proves nothing", col)
		}
		if copied.String != src.String {
			t.Errorf("%s: copy has %q; source has %q", col, copied.String, src.String)
		}
	}

	// The reminder schedule is the other half of that requirement, and it IS a table.
	var hours []int
	rows, err := database.Query(
		`SELECT hours_before FROM event_type_reminders WHERE event_type_id = ? ORDER BY hours_before`, got.ID)
	if err != nil {
		t.Fatalf("read copied reminders: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hb int
		if err := rows.Scan(&hb); err != nil {
			t.Fatalf("scan reminder: %v", err)
		}
		hours = append(hours, hb)
	}
	if len(hours) != 2 || hours[0] != 2 || hours[1] != 24 {
		t.Errorf("copied reminder schedule: got %v; want [2 24]", hours)
	}
}

// TestDuplicateEventType_isCreatedInactiveWithThePriceIntact pins the two rules from the
// issue that pull in opposite directions: the copy must NOT be live, and its price must
// NOT be reset. Zeroing the price is the dangerous default — the operator recognises the
// event type, publishes it, and starts giving away a paid meeting.
func TestDuplicateEventType_isCreatedInactiveWithThePriceIntact(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	mustExec(t, database, `
		INSERT INTO event_types (id, user_id, slug, name, duration_minutes, is_active, price_cents, currency)
		VALUES ('src', ?, 'paid-call', 'Paid Call', 30, 1, 12500, 'gbp')`, ownerID)

	rec := duplicate(t, h, ownerKey, "paid-call")
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var got duplicateResponse
	json.Unmarshal(rec.Body.Bytes(), &got) //nolint:errcheck
	if got.IsActive {
		t.Error("response says the copy is active; a copy must never be publishable on creation")
	}

	var isActive, priceCents int
	var currency string
	if err := database.QueryRow(
		`SELECT is_active, price_cents, currency FROM event_types WHERE id = ?`, got.ID).
		Scan(&isActive, &priceCents, &currency); err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if isActive != 0 {
		t.Error("is_active on the copied row is 1; want 0")
	}
	if priceCents != 12500 || currency != "gbp" {
		t.Errorf("price on the copied row: got %d %q; want 12500 \"gbp\"", priceCents, currency)
	}
	// And the source is untouched — the copy is a new row, not a move.
	if err := database.QueryRow(
		`SELECT is_active, price_cents FROM event_types WHERE id = 'src'`).Scan(&isActive, &priceCents); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if isActive != 1 || priceCents != 12500 {
		t.Errorf("the source changed: is_active=%d price_cents=%d; want 1/12500", isActive, priceCents)
	}
}

// TestDuplicateEventType_handlesEveryEventTypeColumn is a drift gate, not a behaviour
// test. DuplicateEventType names its columns explicitly (SQLite has no "copy every
// column" form), so a migration that adds one would silently leave it out of every
// future copy — a data-loss bug with no failing test anywhere near it.
//
// Adding a column to event_types therefore means adding it to ONE of the two lists
// below: to the INSERT … SELECT in event_type_duplicate.go (the normal answer), or here
// to notInherited with a reason.
func TestDuplicateEventType_handlesEveryEventTypeColumn(t *testing.T) {
	_, database, _, _ := setupWorkspaceWithDB(t)

	// Copied by the INSERT … SELECT in DuplicateEventType.
	copied := map[string]bool{
		"user_id": true, "team_id": true, "name": true, "description": true,
		"duration_minutes": true, "slot_interval_minutes": true,
		"location_type": true, "location_value": true,
		"routing_mode": true, "rr_strategy": true,
		"buffer_before_minutes": true, "buffer_after_minutes": true,
		"min_notice_minutes": true, "max_future_days": true,
		"max_active_bookings": true, "seat_limit": true,
		"is_public": true, "show_taken_slots": true,
		"msg_confirmation": true, "msg_cancellation": true, "msg_reschedule": true,
		"msg_reminder": true, "msg_greeting": true,
		"subj_confirmation": true, "subj_cancellation": true, "subj_reschedule": true,
		"subj_reminder": true,
		"price_cents":   true, "currency": true,
	}
	// Deliberately not inherited, with the reason.
	notInherited := map[string]string{
		"id":          "a copy is a new row",
		"slug":        "UNIQUE; regenerated as <slug>-copy[-N]",
		"is_active":   "forced to 0 so a copy is never published by accident",
		"archived_at": "cleared; archiving belongs to the original's lifecycle",
		"created_at":  "the copy was created now, not when the original was",
	}

	rows, err := database.Query(`SELECT name FROM pragma_table_info('event_types')`)
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pragma rows: %v", err)
	}
	if len(columns) == 0 {
		t.Fatal("pragma_table_info returned no columns for event_types")
	}

	seen := map[string]bool{}
	for _, col := range columns {
		seen[col] = true
		if copied[col] {
			continue
		}
		if _, ok := notInherited[col]; ok {
			continue
		}
		t.Errorf("event_types.%s is handled by neither list: add it to the INSERT … SELECT in "+
			"DuplicateEventType (and to `copied` here), or to `notInherited` with a reason", col)
	}
	for col := range copied {
		if !seen[col] {
			t.Errorf("event_types.%s no longer exists — drop it from `copied` and from the "+
				"INSERT … SELECT in DuplicateEventType", col)
		}
	}
	for col := range notInherited {
		if !seen[col] {
			t.Errorf("event_types.%s no longer exists — drop it from `notInherited`", col)
		}
	}
}
