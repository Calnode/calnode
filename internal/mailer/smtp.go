package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// defaultSMTPTimeout bounds the entire SMTP exchange, dial included, when ctx carries no
// deadline of its own. net/smtp's Client has no context support past the initial dial -
// every call after that blocks on the raw connection with no timeout unless one is set
// here, which previously meant a stalled or misconfigured server (e.g. a port/TLS-mode
// mismatch) could hang the request, and therefore the caller, forever with no error
// surfaced. A var, not a const, so tests can shrink it instead of waiting 30s.
//
// The deadline MUST also be set on the dialer, not only via conn.SetDeadline after the
// dial returns. Several hosting platforms (Railway below Pro, and others) silently drop
// outbound SMTP packets rather than refusing the connection, so the TCP handshake gets no
// answer at all and the dial falls back to the OS SYN-retry limit - roughly 2 minutes on
// Linux. That is far past this timeout, and it stalls the background job queue, which
// shares a single SQLite connection: one unreachable mail host holds up every reminder
// behind it. Bounding the dial turns a 2-minute hang into a prompt, reportable failure.
var defaultSMTPTimeout = 30 * time.Second

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

// ErrUnreachable marks a failure to establish a TCP connection to the SMTP host at all,
// as opposed to the host answering and then rejecting us (bad credentials, relay denied,
// TLS mismatch). The distinction is worth surfacing: an unreachable host on an otherwise
// healthy network usually means the platform is blocking outbound SMTP, which no amount of
// correcting the username and password will fix. Callers use errors.Is to give the admin
// that specific advice instead of a generic "failed to send".
var ErrUnreachable = errors.New("smtp host unreachable")

// newDialers builds the dialers Send uses, both bounded by deadline. Extracted so a test
// can assert the DIAL is bounded and not just the post-connect conversation - see the
// comment on defaultSMTPTimeout for why an unbounded dial is a real production problem.
func newDialers(deadline time.Time, host string) (net.Dialer, tls.Dialer) {
	return net.Dialer{Deadline: deadline},
		tls.Dialer{
			NetDialer: &net.Dialer{Deadline: deadline},
			Config:    &tls.Config{ServerName: host},
		}
}

func (s *SMTP) Send(ctx context.Context, msg Message) error {
	addr := net.JoinHostPort(s.host, s.port)
	raw := s.buildRaw(msg)

	// Bounds the whole conversation, not just the dial (see defaultSMTPTimeout).
	// Whichever is EARLIER of "ctx's own deadline" and "now + the default" wins, so a
	// caller-supplied shorter deadline is still honoured.
	deadline := time.Now().Add(defaultSMTPTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	var c *smtp.Client

	tcpDialer, tlsDialer := newDialers(deadline, s.host)

	if s.implicitTLS {
		d := tlsDialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("mailer: tls dial %s: %w: %w", addr, ErrUnreachable, err)
		}
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
			return fmt.Errorf("mailer: set deadline: %w", err)
		}
		c, err = smtp.NewClient(conn, s.host)
		if err != nil {
			conn.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
			return fmt.Errorf("mailer: smtp client: %w", err)
		}
	} else {
		nd := tcpDialer
		conn, err := nd.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("mailer: dial %s: %w: %w", addr, ErrUnreachable, err)
		}
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
			return fmt.Errorf("mailer: set deadline: %w", err)
		}
		c, err = smtp.NewClient(conn, s.host)
		if err != nil {
			conn.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
			return fmt.Errorf("mailer: smtp client: %w", err)
		}
		if s.startTLS {
			if err := c.StartTLS(&tls.Config{ServerName: s.host}); err != nil {
				c.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
				return fmt.Errorf("mailer: starttls: %w", err)
			}
		}
	}
	defer c.Close()

	if s.username != "" {
		// Our specific server refuses PLAIN and requires LOGIN
		auth := LoginAuth(s.username, s.password)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mailer: SMTP authentication failed: %w", err)
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
		wc.Close() // #nosec G104 -- already returning a more specific error; nothing actionable on close error
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

	hasHTML := msg.HTML != ""
	hasAtt := len(msg.Attachments) > 0

	// Simplest case: plain text, no attachments — a single text/plain message.
	if !hasHTML && !hasAtt {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		buf.WriteString(msg.Text)
		return buf.Bytes()
	}

	// writeBody emits the message body at the current MIME level: either a single
	// text/plain part, or — when an HTML alternative exists — a multipart/alternative
	// container holding the text fallback first and the HTML second (clients pick the
	// richest they support, which is the last). Boundaries are random to avoid
	// colliding with body content.
	writeBody := func(b *bytes.Buffer) {
		if !hasHTML {
			b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
			b.WriteString(msg.Text)
			return
		}
		alt := "alt-" + uid.New()
		fmt.Fprintf(b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", alt)
		fmt.Fprintf(b, "--%s\r\n", alt)
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.Text)
		b.WriteString("\r\n")
		fmt.Fprintf(b, "--%s\r\n", alt)
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.HTML)
		b.WriteString("\r\n")
		fmt.Fprintf(b, "--%s--\r\n", alt)
	}

	// No attachments: the body is the whole message.
	if !hasAtt {
		writeBody(&buf)
		return buf.Bytes()
	}

	// multipart/mixed: the body part (text or multipart/alternative) followed by
	// each attachment.
	mixed := "mixed-" + uid.New()
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed)
	fmt.Fprintf(&buf, "--%s\r\n", mixed)
	writeBody(&buf)
	buf.WriteString("\r\n")
	for _, a := range msg.Attachments {
		fmt.Fprintf(&buf, "--%s\r\n", mixed)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", a.ContentType)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=%q\r\n", a.Filename)
		buf.WriteString("\r\n")
		buf.WriteString(base64Wrap(a.Content))
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", mixed)
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
