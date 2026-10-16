package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

// fakeResend is a stand-in for the parts of Resend's API that RSVP tracking uses. It
// records every call as "METHOD /path".
type fakeResend struct {
	mu       sync.Mutex
	calls    []string
	webhooks []map[string]any
	patched  map[string]any
	created  map[string]any
}

func (f *fakeResend) start(t *testing.T, apiKey string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/raw/em_1" { // the signed download URL carries no API key
			_, _ = w.Write([]byte("From: guest@booker.example\r\n\r\nhello"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"statusCode":401,"name":"restricted_api_key","message":"This API key is restricted to only send emails."}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/webhooks":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": f.webhooks})
		case r.Method == http.MethodPost && r.URL.Path == "/webhooks":
			_ = json.NewDecoder(r.Body).Decode(&f.created)
			_, _ = w.Write([]byte(`{"object":"webhook","id":"wh_new","signing_secret":"whsec_new"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/webhooks/wh_old":
			_ = json.NewDecoder(r.Body).Decode(&f.patched)
			_, _ = w.Write([]byte(`{"object":"webhook","id":"wh_old"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/webhooks/wh_old":
			_, _ = w.Write([]byte(`{"object":"webhook","id":"wh_old","signing_secret":"whsec_old"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/emails/receiving/em_1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "email", "id": "em_1", "from": "Guest <guest@booker.example>",
				"authentication": map[string]string{"spf": "pass", "dkim": "pass", "dmarc": "gray"},
				"raw":            map[string]string{"download_url": "http://" + r.Host + "/raw/em_1"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	prev := resendAPIBase
	resendAPIBase = srv.URL
	t.Cleanup(func() { resendAPIBase = prev })
}

const inboundURL = "https://book.example.com/v1/email/inbound/resend"

func TestEnsureResendInboundWebhook_createsWhenMissing(t *testing.T) {
	f := &fakeResend{webhooks: []map[string]any{
		{"id": "wh_other", "endpoint": "https://other.example.com/hook", "status": "enabled", "events": []string{"email.sent"}},
	}}
	f.start(t, "re_full")

	secret, created, err := EnsureResendInboundWebhook(context.Background(), "re_full", inboundURL)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if secret != "whsec_new" || !created {
		t.Errorf("got secret %q created %v; want the new webhook's secret", secret, created)
	}
	if f.created["endpoint"] != inboundURL || !slices.Equal(toStrings(f.created["events"]), []string{"email.received"}) {
		t.Errorf("created webhook = %v; want our endpoint subscribed to email.received", f.created)
	}
}

// Running setup again, or after someone added the webhook by hand, must reuse it: a
// second webhook would deliver every reply twice.
func TestEnsureResendInboundWebhook_reusesAndRepairsExisting(t *testing.T) {
	f := &fakeResend{webhooks: []map[string]any{
		{"id": "wh_old", "endpoint": inboundURL, "status": "disabled", "events": []string{"email.bounced"}},
	}}
	f.start(t, "re_full")

	secret, created, err := EnsureResendInboundWebhook(context.Background(), "re_full", inboundURL)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if secret != "whsec_old" || created {
		t.Errorf("got secret %q created %v; want the existing webhook's secret, nothing created", secret, created)
	}
	if slices.Contains(f.calls, "POST /webhooks") {
		t.Errorf("created a duplicate webhook: calls %v", f.calls)
	}
	events := toStrings(f.patched["events"])
	if f.patched["status"] != "enabled" || !slices.Contains(events, "email.received") || !slices.Contains(events, "email.bounced") {
		t.Errorf("patch = %v; want it enabled with email.received added and existing events kept", f.patched)
	}
}

// A sending-only key is the common mistake: it must come back as the error the settings
// page can explain, not as a generic 401.
func TestEnsureResendInboundWebhook_sendingOnlyKey(t *testing.T) {
	f := &fakeResend{}
	f.start(t, "re_full")
	_, _, err := EnsureResendInboundWebhook(context.Background(), "re_sending_only", inboundURL)
	if !errors.Is(err, ErrResendRestrictedKey) {
		t.Errorf("got %v; want ErrResendRestrictedKey", err)
	}
}

// The sender check that guards RSVPs reads Resend's authentication results; misreading
// them would silently drop every answer (or, worse, trust unverified ones).
func TestFetchReceived_returnsSenderAndAuthentication(t *testing.T) {
	f := &fakeResend{}
	f.start(t, "re_full")
	got, err := FetchReceived(context.Background(), "re_full", "em_1")
	if err != nil {
		t.Fatalf("FetchReceived: %v", err)
	}
	if got.From != "Guest <guest@booker.example>" || string(got.Raw) != "From: guest@booker.example\r\n\r\nhello" {
		t.Errorf("got From %q raw %q", got.From, got.Raw)
	}
	if got.DKIM != "pass" || got.DMARC != "gray" || !got.SenderAuthenticated() {
		t.Errorf("authentication = %+v; want DKIM pass to authenticate the sender", got)
	}
	if _, err := FetchReceived(context.Background(), "re_sending_only", "em_1"); !errors.Is(err, ErrResendRestrictedKey) {
		t.Errorf("sending-only key: got %v; want ErrResendRestrictedKey", err)
	}
}

func toStrings(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, s := range list {
			if str, ok := s.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}
