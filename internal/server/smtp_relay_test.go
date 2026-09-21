package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/handler"
)

func TestSMTPRelayAtBootAndSettingsReload(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored=%v", stored), func(t *testing.T) {
			names := make(chan string, 2)
			relay := httptest.NewUnstartedServer(http.NotFoundHandler())
			relay.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				names <- hello.ServerName
				return nil, errors.New("test rejects handshake")
			}}
			relay.StartTLS()
			defer relay.Close()
			host, port, err := net.SplitHostPort(relay.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			database, err := db.Open("sqlite://:memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := db.Migrate(database); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			seed := handler.New(database, logger)
			rec := httptest.NewRecorder()
			seed.Setup(rec, httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(`{"name":"Host","email":"host@example.com","timezone":"UTC"}`)))
			if rec.Code != http.StatusCreated {
				t.Fatalf("setup: %d %s", rec.Code, rec.Body)
			}
			var identity struct {
				APIKey string `json:"api_key"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &identity); err != nil {
				t.Fatal(err)
			}
			patch := func(h *handler.Handler, smtpHost string) {
				t.Helper()
				req := httptest.NewRequest(http.MethodPatch, "/v1/settings/email", strings.NewReader(fmt.Sprintf(`{"smtp_host":%q,"smtp_port":"465","smtp_tls":true,"email_from":"from@example.com"}`, smtpHost)))
				req.Header.Set("Authorization", "Bearer "+identity.APIKey)
				rec := httptest.NewRecorder()
				h.RequireAuth(h.PatchEmailSettings)(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("settings: %d %s", rec.Code, rec.Body)
				}
			}
			if stored {
				patch(seed, "smtp.example.com")
			}
			ctx, cancel := context.WithCancel(t.Context())
			h, drain := BuildHandler(ctx, &config.Config{
				BaseURL: "https://calnode.localhost", DataDir: t.TempDir(),
				SMTPHost: "smtp.example.com", SMTPPort: "465", SMTPTLS: true,
				EmailFrom: "from@example.com", SMTPConnectHost: host, SMTPConnectPort: port,
			}, database, logger)
			defer func() { cancel(); drain() }()
			for _, smtpHost := range []string{"smtp.example.com", "updated.example.com"} {
				if smtpHost == "updated.example.com" {
					patch(h, smtpHost)
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/settings/email/test", nil)
				req.Header.Set("Authorization", "Bearer "+identity.APIKey)
				rec := httptest.NewRecorder()
				h.RequireAuth(h.TestEmailConnection)(rec, req)
				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("test email: %d %s", rec.Code, rec.Body)
				}
				select {
				case name := <-names:
					if name != smtpHost {
						t.Fatalf("TLS name = %q, want %q", name, smtpHost)
					}
				case <-time.After(time.Second):
					t.Fatal("test email did not reach relay")
				}
			}
			cancel()
			drain()
			if !strings.Contains(logs.String(), "SMTP relay active") || !strings.Contains(logs.String(), relay.Listener.Addr().String()) {
				t.Fatal("relay configuration was not logged")
			}
		})
	}
}
