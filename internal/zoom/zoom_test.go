package zoom

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/secret"
)

const testKeyHex = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("newTestDB: open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("newTestDB: migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(newTestDB(t), "client-id", "client-secret", "http://localhost/callback", testKeyHex)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func seedUser(t *testing.T, database *sql.DB, userID string) {
	t.Helper()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		VALUES (?, ?, 'Test Host', 'UTC', 0, '2026-01-01T00:00:00Z')`,
		userID, userID+"@test.example")
	if err != nil {
		t.Fatalf("seedUser(%q): %v", userID, err)
	}
}

// connectZoom inserts a non-expired Zoom token row for userID.
func connectZoom(t *testing.T, database *sql.DB, userID, accessToken string) {
	t.Helper()
	key, _ := secret.ParseKey(testKeyHex)
	enc, err := secret.Encrypt(key, accessToken)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	_, err = database.ExecContext(context.Background(), `
		INSERT INTO zoom_connections (user_id, access_token_enc, refresh_token_enc, expiry_at, created_at)
		VALUES (?, ?, '', ?, ?)`,
		userID, enc,
		time.Now().Add(time.Hour).Format(time.RFC3339),
		time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("connectZoom: %v", err)
	}
}

func TestCreateMeeting(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":83746522,"join_url":"https://zoom.us/j/83746522"}`)
	}))
	defer srv.Close()

	c := newTestClient(t)
	c.apiBase = srv.URL
	seedUser(t, c.db, "host-1")
	connectZoom(t, c.db, "host-1", "access-tok")

	join, mid, err := c.CreateMeeting(context.Background(), "host-1", MeetingParams{
		Topic:           "Intro with Alex",
		Start:           time.Date(2026, 6, 25, 21, 0, 0, 0, time.UTC),
		DurationMinutes: 30,
		Timezone:        "Pacific/Auckland",
	})
	if err != nil {
		t.Fatalf("CreateMeeting: %v", err)
	}
	if join != "https://zoom.us/j/83746522" {
		t.Errorf("join_url = %q", join)
	}
	if mid != "83746522" { // numeric id stringified
		t.Errorf("meeting id = %q; want 83746522", mid)
	}
	if gotMethod != http.MethodPost || gotPath != "/users/me/meetings" {
		t.Errorf("request = %s %s; want POST /users/me/meetings", gotMethod, gotPath)
	}
	if gotAuth != "Bearer access-tok" {
		t.Errorf("auth = %q; want Bearer access-tok", gotAuth)
	}
	if gotBody["start_time"] != "2026-06-25T21:00:00Z" {
		t.Errorf("start_time = %v; want 2026-06-25T21:00:00Z", gotBody["start_time"])
	}
	if d, _ := gotBody["duration"].(float64); d != 30 {
		t.Errorf("duration = %v; want 30", gotBody["duration"])
	}
}

func TestCreateMeeting_notConnected(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host-2")
	if _, _, err := c.CreateMeeting(context.Background(), "host-2", MeetingParams{Topic: "x", DurationMinutes: 30}); err == nil {
		t.Error("expected an error when the host has no Zoom connection")
	}
}

func TestDeleteMeeting_idempotentOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // already gone
	}))
	defer srv.Close()
	c := newTestClient(t)
	c.apiBase = srv.URL
	seedUser(t, c.db, "host-3")
	connectZoom(t, c.db, "host-3", "tok")
	if err := c.DeleteMeeting(context.Background(), "host-3", "999"); err != nil {
		t.Errorf("DeleteMeeting should treat 404 as success, got: %v", err)
	}
}
