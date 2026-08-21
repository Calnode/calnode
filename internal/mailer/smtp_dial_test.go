package mailer

import (
	"context"
	"testing"
	"time"
)

// TestNewDialers_boundsTheDialItself guards a real regression. defaultSMTPTimeout was
// applied only via conn.SetDeadline, which runs AFTER the dial returns, so the dial was
// bounded by nothing but the OS SYN-retry limit (~2 minutes on Linux). That matters because
// several hosting platforms silently drop outbound SMTP rather than refusing it, so the
// handshake gets no answer at all - and the mailer runs on the background job queue, which
// shares a single SQLite connection, so one unreachable host stalls every queued email.
func TestNewDialers_boundsTheDialItself(t *testing.T) {
	deadline := time.Now().Add(7 * time.Second)
	tcp, tlsD := newDialers(deadline, "smtp.example.com")

	if tcp.Deadline != deadline {
		t.Errorf("plain TCP dialer deadline = %v, want %v (an unset deadline means the dial "+
			"falls back to the OS SYN-retry limit)", tcp.Deadline, deadline)
	}
	if tlsD.NetDialer == nil {
		t.Fatal("TLS dialer has no NetDialer, so its dial is unbounded")
	}
	if tlsD.NetDialer.Deadline != deadline {
		t.Errorf("TLS dialer deadline = %v, want %v", tlsD.NetDialer.Deadline, deadline)
	}
	if tlsD.Config == nil || tlsD.Config.ServerName != "smtp.example.com" {
		t.Errorf("TLS dialer must still verify the server name; got %+v", tlsD.Config)
	}
}

// TestSend_unreachableHostFailsPromptly is the behavioural half: a send to an address that
// never answers must return within the timeout rather than hanging.
//
// 203.0.113.0/24 is TEST-NET-3 (RFC 5737) and is not routable, so the connection either
// blackholes (the case we care about) or is rejected immediately by the local stack. Both
// outcomes return fast, so this cannot flake on a network that refuses quickly - it only
// fails if the dial is genuinely unbounded, which is the regression.
func TestSend_unreachableHostFailsPromptly(t *testing.T) {
	orig := defaultSMTPTimeout
	defaultSMTPTimeout = 500 * time.Millisecond
	t.Cleanup(func() { defaultSMTPTimeout = orig })

	s := NewSMTP("203.0.113.1", "587", "user", "pass", false, true, "from@example.com", "From")

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- s.Send(context.Background(), Message{To: []string{"a@example.com"}, Text: "hi"}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error sending to an unroutable address")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("Send took %v; the dial is not bounded by defaultSMTPTimeout", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send hung on an unreachable host: the dial is not bounded by the deadline")
	}
}
