package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSMTPRelayPreservesTLSIdentity(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL_SMTP_CONNECT_HOST", host)
	t.Setenv("EMAIL_SMTP_CONNECT_PORT", port)
	names := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		server := tls.Server(conn, &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			names <- hello.ServerName
			return nil, errors.New("test rejects TLS handshake")
		}})
		_ = server.Handshake()
	}()
	sender := NewSMTP("smtp.example.com", "465", "", "", true, false, "from@example.com", "Test")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := sender.Send(ctx, Message{To: []string{"to@example.com"}, Subject: "test"}); err == nil {
		t.Fatal("accepted a rejected TLS handshake")
	}
	select {
	case name := <-names:
		if name != "smtp.example.com" {
			t.Fatalf("TLS server name = %q", name)
		}
	case <-ctx.Done():
		t.Fatal("did not connect to relay")
	}
}
