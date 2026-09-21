package mailer

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPRelayPreservesTLSIdentity(t *testing.T) {
	for _, startTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("startTLS=%v", startTLS), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			host, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			names := make(chan string, 1)
			serverResult := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverResult <- err
					return
				}
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					serverResult <- err
					return
				}
				if startTLS {
					reader := bufio.NewReader(conn)
					for _, step := range []struct{ reply, command string }{
						{"220 smtp.example.com ready\r\n", "EHLO "},
						{"250-smtp.example.com\r\n250 STARTTLS\r\n", "STARTTLS\r\n"},
					} {
						if _, err := fmt.Fprint(conn, step.reply); err != nil {
							serverResult <- err
							return
						}
						line, err := reader.ReadString('\n')
						if err != nil || !strings.HasPrefix(line, step.command) {
							serverResult <- fmt.Errorf("command %q: %v", line, err)
							return
						}
					}
					if _, err := fmt.Fprint(conn, "220 begin TLS\r\n"); err != nil {
						serverResult <- err
						return
					}
				}
				server := tls.Server(conn, &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
					names <- hello.ServerName
					return nil, errors.New("test rejects TLS handshake")
				}})
				if err := server.Handshake(); err == nil {
					serverResult <- errors.New("handshake unexpectedly succeeded")
					return
				}
				serverResult <- nil
			}()
			sender := NewSMTP("smtp.example.com", "465", host, port, "", "", !startTLS, startTLS, "from@example.com", "Test")
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
			if err := <-serverResult; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSMTPConnectAddressDefaults(t *testing.T) {
	for _, tc := range []struct{ host, port, wantHost, wantPort string }{
		{"", "", "smtp.example.com", "465"},
		{"relay.example.com", "", "relay.example.com", "465"},
		{"", "2525", "smtp.example.com", "2525"},
	} {
		s := NewSMTP("smtp.example.com", "465", tc.host, tc.port, "", "", true, false, "from@example.com", "Test")
		if s.connectHost != tc.wantHost || s.connectPort != tc.wantPort {
			t.Fatalf("connect address = %s:%s, want %s:%s", s.connectHost, s.connectPort, tc.wantHost, tc.wantPort)
		}
	}
}
