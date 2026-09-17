package handler_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/caldav"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/handler"
)

// twoCalendarDAV is a CalDAV server whose account holds two event calendars.
func twoCalendarDAV(t *testing.T) *httptest.Server {
	t.Helper()
	const ns = `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`
	cal := func(href, name string) string {
		return `<d:response><d:href>` + href + `</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop>` +
			`<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>` + name + `</d:displayname>` +
			`<c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set>` +
			`</d:prop></d:propstat></d:response>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusMultiStatus)
		switch {
		case strings.Contains(string(body), "current-user-principal"):
			io.WriteString(w, ns+`<d:response><d:href>`+r.URL.Path+`</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:current-user-principal><d:href>/p/</d:href></d:current-user-principal></d:prop></d:propstat></d:response></d:multistatus>`)
		case strings.Contains(string(body), "calendar-home-set"):
			io.WriteString(w, ns+`<d:response><d:href>/p/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><c:calendar-home-set><d:href>/cal/</d:href></c:calendar-home-set></d:prop></d:propstat></d:response></d:multistatus>`)
		default:
			io.WriteString(w, ns+cal("/cal/home/", "Home")+cal("/cal/work/", "Work")+`</d:multistatus>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// PUT /v1/calendar/connections/{id}/calendars must refuse a calendar the provider did not list,
// with a 400 that says what to do, and still accept one it did.
func TestPutConnectionCalendars_caldavRefusesAnUnlistedCalendar(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	h := handler.New(database, slog.Default())
	cc, err := caldav.New(database, testGCalKeyHex)
	if err != nil {
		t.Fatalf("caldav.New: %v", err)
	}
	svc := calendar.NewService(database)
	svc.Register(cc)
	h.SetCalendar(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(`{"name":"Cal User","email":"cal@example.com","timezone":"UTC"}`))
	req.Header.Set("Content-Type", "application/json")
	h.Setup(rec, req)
	var setup struct {
		APIKey string `json:"api_key"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &setup) != nil {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}

	// Connect through the real handler and client: the loopback test server passes the
	// metadata-only guard, which is the guard every listing and read goes through.
	dav := twoCalendarDAV(t)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ConnectCalDAV)(rec, authReq(http.MethodPost, "/v1/calendar/caldav/connect",
		`{"server_url":"`+dav.URL+`","username":"me@example.com","app_password":"pw"}`, setup.APIKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.RequireAuth(h.GetConnectionCalendars)(rec, authReq(http.MethodGet,
		"/v1/calendar/connections/x/calendars?provider=caldav&account=me%40example.com", "", setup.APIKey))
	var listed struct {
		Calendars []calendar.CalendarSelection `json:"calendars"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Calendars) != 2 {
		t.Fatalf("list: %d %s, want both calendars", rec.Code, rec.Body.String())
	}

	put := func(calendarID string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{
			"provider": "caldav", "account_email": "me@example.com",
			"calendars": []map[string]any{{"id": calendarID, "check_conflicts": true, "is_destination": true}},
		})
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PutConnectionCalendars)(rec, authReq(http.MethodPut, "/v1/calendar/connections/x/calendars", string(body), setup.APIKey))
		return rec
	}

	rec = put("http://169.254.169.254/latest/meta-data/")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unlisted calendar: status %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "is not one of this account's calendars") {
		t.Errorf("unlisted calendar: body %s, want a message naming the problem", rec.Body.String())
	}

	if rec = put(listed.Calendars[1].ID); rec.Code != http.StatusNoContent {
		t.Errorf("listed calendar: status %d %s, want 204", rec.Code, rec.Body.String())
	}
}
