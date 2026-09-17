package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
)

// plainProvider is a provider that does not validate selections (Google and Microsoft today).
// Only Name is expected to be called; the embedded nil Provider panics loudly otherwise, and
// ListCalendars is counted so a test can prove saving never lists.
type plainProvider struct {
	Provider
	name   string
	listed int
}

func (p *plainProvider) Name() string { return p.name }

func (p *plainProvider) ListCalendars(context.Context, string, string) ([]CalendarInfo, error) {
	p.listed++
	return nil, errors.New("plainProvider: ListCalendars should not be called when saving")
}

// validatingProvider accepts only the ids it offers, or fails to check at all when err is set.
type validatingProvider struct {
	plainProvider
	offered []string
	err     error
}

func (p *validatingProvider) ValidateSelection(_ context.Context, _, _ string, ids []string) error {
	if p.err != nil {
		return p.err
	}
	for _, id := range ids {
		if !slices.Contains(p.offered, id) {
			return &UnknownCalendarError{CalendarID: id}
		}
	}
	return nil
}

func selection(id string, check, dest bool) CalendarSelection {
	return CalendarSelection{CalendarInfo: CalendarInfo{ID: id}, CheckConflicts: check, IsDestination: dest}
}

func savedSelection(t *testing.T, db *sql.DB, account string) (checked []string, dest string) {
	t.Helper()
	rows, err := db.Query(`SELECT calendar_id, check_conflicts, is_destination FROM connection_calendars
		WHERE user_id = 'u1' AND account_email = ? ORDER BY calendar_id`, account)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var cc, d int
		if err := rows.Scan(&id, &cc, &d); err != nil {
			t.Fatal(err)
		}
		if cc != 0 {
			checked = append(checked, id)
		}
		if d != 0 {
			dest = id
		}
	}
	return checked, dest
}

// A provider that does not validate selections saves exactly what it is given, with no call
// to the provider: Google's "primary" alias, which its calendar list never returns, still saves.
func TestSetAccountCalendars_nonValidatingProviderSavesAsGiven(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "c1", "u1", "google", "work@x.test", 1, 1)
	p := &plainProvider{name: "google"}
	svc := NewService(db)
	svc.Register(p)

	if err := svc.SetAccountCalendars(context.Background(), "u1", "google", "work@x.test", []CalendarSelection{
		selection("primary", true, true),
		selection("not-in-any-list@x.test", true, false),
	}); err != nil {
		t.Fatalf("SetAccountCalendars: %v", err)
	}
	if p.listed != 0 {
		t.Errorf("ListCalendars called %d time(s); saving must stay a local write for this provider", p.listed)
	}
	checked, dest := savedSelection(t, db, "work@x.test")
	if want := []string{"not-in-any-list@x.test", "primary"}; !slices.Equal(checked, want) || dest != "primary" {
		t.Errorf("saved checked=%v dest=%q, want checked=%v dest=primary", checked, dest, want)
	}
}

// A validating provider's refusal saves nothing, leaving the previous selection as it was.
func TestSetAccountCalendars_validatingProviderRefusalSavesNothing(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "c1", "u1", "caldav", "me@x.test", 1, 1)
	p := &validatingProvider{plainProvider: plainProvider{name: "caldav"},
		offered: []string{"https://dav.x.test/cal/home/", "https://dav.x.test/cal/work/"}}
	svc := NewService(db)
	svc.Register(p)
	ctx := context.Background()

	if err := svc.SetAccountCalendars(ctx, "u1", "caldav", "me@x.test", []CalendarSelection{
		selection("https://dav.x.test/cal/home/", true, false),
		selection("https://dav.x.test/cal/work/", true, true),
	}); err != nil {
		t.Fatalf("offered selection: %v", err)
	}

	err := svc.SetAccountCalendars(ctx, "u1", "caldav", "me@x.test", []CalendarSelection{
		selection("https://dav.x.test/cal/home/", false, true),
		selection("https://elsewhere.test/steal/", true, false),
	})
	var unknown *UnknownCalendarError
	if !errors.As(err, &unknown) || unknown.CalendarID != "https://elsewhere.test/steal/" {
		t.Fatalf("err = %v, want *UnknownCalendarError for https://elsewhere.test/steal/", err)
	}
	if errors.Is(err, ErrCalendarList) {
		t.Errorf("err = %v; a refused id is not a listing failure", err)
	}
	checked, dest := savedSelection(t, db, "me@x.test")
	if want := []string{"https://dav.x.test/cal/home/", "https://dav.x.test/cal/work/"}; !slices.Equal(checked, want) || dest != "https://dav.x.test/cal/work/" {
		t.Errorf("after a refused save checked=%v dest=%q, want the earlier selection %v with dest work", checked, dest, want)
	}
}

// When a validating provider cannot check at all, nothing is saved and the error says so: a
// caller maps ErrCalendarList to "provider unreachable" and a rejected login to "reconnect".
func TestSetAccountCalendars_validationFailureIsErrCalendarList(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "c1", "u1", "caldav", "me@x.test", 1, 1)
	svc := NewService(db)
	svc.Register(&validatingProvider{plainProvider: plainProvider{name: "caldav"},
		err: fmt.Errorf("caldav: authentication failed: %w", ErrReauthRequired)})

	err := svc.SetAccountCalendars(context.Background(), "u1", "caldav", "me@x.test",
		[]CalendarSelection{selection("https://dav.x.test/cal/home/", true, true)})
	if !errors.Is(err, ErrCalendarList) {
		t.Errorf("err = %v, want it to wrap ErrCalendarList", err)
	}
	if !IsReauthErr(err) {
		t.Errorf("err = %v, want the provider's reauth error still recognisable", err)
	}
	if checked, dest := savedSelection(t, db, "me@x.test"); len(checked) != 0 || dest != "" {
		t.Errorf("saved checked=%v dest=%q although the selection could not be checked", checked, dest)
	}
}
