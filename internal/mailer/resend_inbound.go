package mailer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Resend delivers received email ("inbound") as an email.received webhook, signed the Svix
// way. The webhook carries metadata only; the message itself is fetched from the
// Received emails API, whose raw.download_url serves the original RFC 5322 bytes.

// ErrWebhookSignature means a webhook request is not provably from Resend: a missing or
// wrong signature, or a timestamp outside the replay window.
var ErrWebhookSignature = errors.New("resend webhook: signature verification failed")

// webhookTolerance is the replay window Svix itself uses.
const webhookTolerance = 5 * time.Minute

// VerifyResendWebhook checks a Resend (Svix) webhook signature against the endpoint's
// signing secret ("whsec_<base64>"). body must be the raw request body, unparsed.
func VerifyResendWebhook(secret string, header http.Header, body []byte, now time.Time) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 {
		return fmt.Errorf("%w: unusable signing secret", ErrWebhookSignature)
	}
	id, ts, sigs := header.Get("svix-id"), header.Get("svix-timestamp"), header.Get("svix-signature")
	if id == "" || ts == "" || sigs == "" {
		return fmt.Errorf("%w: missing svix headers", ErrWebhookSignature)
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp", ErrWebhookSignature)
	}
	if d := now.Sub(time.Unix(sec, 0)); d > webhookTolerance || d < -webhookTolerance {
		return fmt.Errorf("%w: timestamp outside tolerance", ErrWebhookSignature)
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	// The header lists one or more "v1,<base64>" signatures (several during secret rotation).
	for _, s := range strings.Fields(sigs) {
		version, sig, ok := strings.Cut(s, ",")
		if !ok || version != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(sig)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return ErrWebhookSignature
}

// ResendInboundEvent is the part of an email.received webhook Calnode reads.
type ResendInboundEvent struct {
	Type string `json:"type"`
	Data struct {
		EmailID     string   `json:"email_id"`
		From        string   `json:"from"`
		To          []string `json:"to"`
		CC          []string `json:"cc"`
		ReceivedFor []string `json:"received_for"`
	} `json:"data"`
}

// Recipients returns every address the message was delivered to.
func (e ResendInboundEvent) Recipients() []string {
	out := make([]string, 0, len(e.Data.To)+len(e.Data.CC)+len(e.Data.ReceivedFor))
	out = append(out, e.Data.To...)
	out = append(out, e.Data.CC...)
	return append(out, e.Data.ReceivedFor...)
}

// resendReceivingEndpoint is the Received emails API. A var so tests can stub it.
var resendReceivingEndpoint = "https://api.resend.com/emails/receiving/"

// maxRawEmail bounds a downloaded message. An RSVP is a few KB; this only stops a
// pathological message from being read into memory whole.
const maxRawEmail = 5 << 20

// FetchReceivedRaw downloads the original bytes of a received email: one API call for the
// short-lived signed URL, one GET for the message.
func FetchReceivedRaw(ctx context.Context, apiKey, emailID string) ([]byte, error) {
	if apiKey == "" {
		return nil, errors.New("resend inbound: api key not configured")
	}
	client := &http.Client{Timeout: resendTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resendReceivingEndpoint+url.PathEscape(emailID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resend inbound: retrieve email: %w", err)
	}
	var meta struct {
		Raw *struct {
			DownloadURL string `json:"download_url"`
		} `json:"raw"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta)
	resp.Body.Close() // #nosec G104 -- body fully consumed above; a close error changes nothing
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resend inbound: retrieve email: status %d", resp.StatusCode)
	}
	if decodeErr != nil || meta.Raw == nil || meta.Raw.DownloadURL == "" {
		return nil, errors.New("resend inbound: retrieve email: no raw download url")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, meta.Raw.DownloadURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resend inbound: download raw email: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resend inbound: download raw email: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxRawEmail))
}
