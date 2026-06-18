package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"

	"github.com/calnode/calnode/internal/uid"
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
			// Don't wrap err — SMTP auth responses can contain server-side
			// detail that may expose credential information in logs.
			return fmt.Errorf("mailer: SMTP authentication failed")
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
		// wc.Close() sends the DATA terminator (CRLF.CRLF) and reads the
		// server's 250 OK. If this succeeds the message has been accepted.
		return fmt.Errorf("mailer: close DATA writer: %w", err)
	}

	// Quit is best-effort: once wc.Close() succeeded the server accepted the
	// message. A broken connection at this point does not mean the email was
	// lost, so we ignore the Quit error.
	_ = c.Quit()
	return nil
}

func (s *SMTP) buildRaw(msg Message) []byte {
	from := mail.Address{Name: s.fromName, Address: s.from}

	// mime.QEncoding.Encode returns the string unchanged when it is pure ASCII
	// (no control characters, no bytes > 0x7E). When it contains non-ASCII or
	// control characters — including \r and \n that would allow SMTP header
	// injection — it Q-encodes them as =0D / =0A, neutralising the injection
	// and satisfying RFC 2047 at the same time.
	subject := mime.QEncoding.Encode("utf-8", msg.Subject)

	// Validate and normalise To addresses so the To: header line is properly
	// quoted. Delivery uses c.Rcpt() (separate SMTP command) so a To: header
	// formatting error cannot redirect mail.
	toFormatted := make([]string, 0, len(msg.To))
	for _, addr := range msg.To {
		if a, err := mail.ParseAddress(addr); err == nil {
			toFormatted = append(toFormatted, a.String())
		} else {
			toFormatted = append(toFormatted, addr)
		}
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from.String())
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(toFormatted, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	// Simple single-part message (the common case) — unchanged.
	if len(msg.Attachments) == 0 {
		fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(msg.Text)
		return buf.Bytes()
	}

	// multipart/mixed: text body + each attachment. The boundary is random so it
	// can't collide with body/attachment content.
	boundary := "calnode-" + uid.New()
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n", boundary)
	buf.WriteString("\r\n")

	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(msg.Text)
	buf.WriteString("\r\n")

	for _, a := range msg.Attachments {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", a.ContentType)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=%q\r\n", a.Filename)
		buf.WriteString("\r\n")
		buf.WriteString(base64Wrap(a.Content))
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes()
}

// base64Wrap base64-encodes b and wraps it at 76 characters per line (RFC 2045).
func base64Wrap(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	for len(enc) > 76 {
		sb.WriteString(enc[:76])
		sb.WriteString("\r\n")
		enc = enc[76:]
	}
	sb.WriteString(enc)
	return sb.String()
}
