package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"time"
)

// resendEndpoint is the Resend send-email API. A var so tests can point it at a stub.
var resendEndpoint = "https://api.resend.com/emails"

// resendTimeout bounds a single API call. Generous enough for a large .ics attachment on a
// slow link, short enough that a stuck request cannot occupy a queue worker for long.
var resendTimeout = 20 * time.Second

// ErrEmailRejected marks a request the provider understood and refused - an unverified
// sending domain, a malformed address, a revoked key. Retrying sends the identical request
// and gets the identical refusal, so it is worth distinguishing from a transport failure.
//
// The job queue does not act on this yet: it retries every failure with backoff up to
// max_attempts, which bounds the damage but still burns attempts on a request that cannot
// succeed. Wiring the queue to fail fast on this is a worthwhile follow-up, deliberately
// not bundled into the transport change.
var ErrEmailRejected = errors.New("email rejected by provider")

// Resend sends email over Resend's HTTPS API instead of SMTP.
//
// This exists because SMTP is not universally available. Several hosting platforms block
// outbound SMTP on their cheaper plans (Railway below Pro among them) by dropping the
// packets rather than refusing the connection, which is indistinguishable from a
// misconfiguration. Resend's own guidance is to prefer the HTTPS API regardless, since
// port 443 is never blocked.
//
// Deliberately concrete rather than a provider abstraction: Resend is already the
// documented recommendation in DEPLOY.md, and one working transport beats a plugin
// framework built for providers nobody has asked for. A second provider means a second
// file implementing Mailer, not a redesign.
type Resend struct {
	apiKey   string
	from     string
	fromName string
	client   *http.Client
}

// NewResend constructs a Resend API sender. from must be an address on a domain verified
// in the Resend dashboard; the API rejects anything else.
func NewResend(apiKey, from, fromName string) *Resend {
	return &Resend{
		apiKey:   apiKey,
		from:     from,
		fromName: fromName,
		client:   &http.Client{Timeout: resendTimeout},
	}
}

type resendAttachment struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`                // base64
	Type     string `json:"content_type,omitempty"` // see the note in Send
}

type resendPayload struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Subject     string             `json:"subject"`
	Text        string             `json:"text,omitempty"`
	HTML        string             `json:"html,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

// Send delivers msg through the Resend API.
func (r *Resend) Send(ctx context.Context, msg Message) error {
	if r.apiKey == "" {
		return errors.New("mailer: resend api key not configured")
	}

	from := mail.Address{Name: r.fromName, Address: r.from}
	payload := resendPayload{
		From:    from.String(),
		To:      msg.To,
		Subject: msg.Subject,
		Text:    msg.Text,
		HTML:    msg.HTML,
	}

	// Attachments carry their full MIME type, parameters included. This matters more than
	// it looks: calendar invites go out as `text/calendar; ...; method=REQUEST`, and the
	// method parameter is what makes a mail client render an RSVP-able event instead of a
	// file to download. content_type is the documented field for this; setting it through
	// the `headers` field instead is rejected as a duplicate Content-Type.
	//
	// Whether Resend preserves the parameters verbatim is not something their docs commit
	// to. TestICSAttachmentKeepsItsMethodParameter pins what WE send; if invites ever
	// arrive as plain attachments, that is the thing to re-check first, and the fallback is
	// to carry the calendar part in the body rather than as an attachment.
	for _, a := range msg.Attachments {
		payload.Attachments = append(payload.Attachments, resendAttachment{
			Filename: a.Filename,
			Content:  base64.StdEncoding.EncodeToString(a.Content),
			Type:     a.ContentType,
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mailer: resend marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mailer: resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		// A transport failure here is genuinely unreachable-network territory, so it maps
		// onto the same sentinel SMTP uses and the admin gets consistent advice.
		return fmt.Errorf("mailer: resend post: %w: %w", ErrUnreachable, err)
	}
	defer resp.Body.Close() // #nosec G104 -- response body close error is not actionable

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
		return nil
	}

	detail := resendErrorDetail(resp.Body)

	// 429 and 5xx are worth another attempt; a 4xx means the request itself is wrong and
	// resending it unchanged will fail identically.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("mailer: resend api %d: %s", resp.StatusCode, detail)
	}
	return fmt.Errorf("mailer: resend api %d: %w: %s", resp.StatusCode, ErrEmailRejected, detail)
}

// resendErrorDetail pulls the human-readable message out of an error response, falling back
// to the raw body. Bounded read: an error body should be small, and a broken or hostile
// endpoint should not be able to stream us an unbounded string into a log line.
func resendErrorDetail(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 8<<10))
	if err != nil || len(raw) == 0 {
		return "no response body"
	}
	var parsed struct {
		Message string `json:"message"`
		Name    string `json:"name"`
	}
	if json.Unmarshal(raw, &parsed) == nil && parsed.Message != "" {
		if parsed.Name != "" {
			return parsed.Name + ": " + parsed.Message
		}
		return parsed.Message
	}
	return string(raw)
}
