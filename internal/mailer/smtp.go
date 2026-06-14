package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
)

// SMTP sends email via an SMTP server.
type SMTP struct {
	host        string
	port        string
	username    string
	password    string
	implicitTLS bool // port 465 — TLS from the first byte
	startTLS    bool // port 587 — upgrade with STARTTLS
	from        string
	fromName    string
}

// NewSMTP constructs an SMTP sender. implicitTLS selects port-465 mode;
// startTLS selects port-587 STARTTLS mode. Both false means plain SMTP
// (suitable for a local relay on port 25).
func NewSMTP(host, port, username, password string, implicitTLS, startTLS bool, from, fromName string) *SMTP {
	return &SMTP{
		host:        host,
		port:        port,
		username:    username,
		password:    password,
		implicitTLS: implicitTLS,
		startTLS:    startTLS,
		from:        from,
		fromName:    fromName,
	}
}

func (s *SMTP) Send(ctx context.Context, msg Message) error {
	addr := net.JoinHostPort(s.host, s.port)
	raw := s.buildRaw(msg)

	var c *smtp.Client

	if s.implicitTLS {
		d := tls.Dialer{Config: &tls.Config{ServerName: s.host}}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("mailer: tls dial %s: %w", addr, err)
		}
		c, err = smtp.NewClient(conn, s.host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("mailer: smtp client: %w", err)
		}
	} else {
		var nd net.Dialer
		conn, err := nd.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("mailer: dial %s: %w", addr, err)
		}
		c, err = smtp.NewClient(conn, s.host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("mailer: smtp client: %w", err)
		}
		if s.startTLS {
			if err := c.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
				c.Close()
				return fmt.Errorf("mailer: starttls: %w", err)
			}
		}
	}
	defer c.Close()

	if s.username != "" {
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}

	if err := c.Mail(s.from); err != nil {
		return fmt.Errorf("mailer: MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("mailer: RCPT TO %s: %w", to, err)
		}
	}

	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA: %w", err)
	}
	if _, err := wc.Write(raw); err != nil {
		wc.Close()
		return fmt.Errorf("mailer: write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("mailer: close DATA writer: %w", err)
	}

	return c.Quit()
}

func (s *SMTP) buildRaw(msg Message) []byte {
	from := mail.Address{Name: s.fromName, Address: s.from}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from.String())
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(msg.To, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(msg.Text)
	return buf.Bytes()
}
