package mailer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

// maxRawEmail bounds a downloaded message. An RSVP is a few KB; this only stops a
// pathological message from being read into memory whole.
const maxRawEmail = 5 << 20

// ReceivedEmail is a message Resend received: its original bytes plus what Resend itself
// established about the sender. From is the header From address; the authentication
// results are computed by Resend's receiving server, so the sender cannot forge them.
type ReceivedEmail struct {
	Raw  []byte
	From string
	SPF  string // "pass", "fail", "gray", ...; "" when Resend reported none
	DKIM string
	// DMARC "pass" means SPF or DKIM passed aligned with the From domain.
	DMARC string
}

// SenderAuthenticated reports whether Resend verified the From domain: DMARC passed, or
// DKIM passed (Resend's DKIM pass requires the signing domain to match From). A header
// From that nothing vouches for is just text anyone can type.
func (e ReceivedEmail) SenderAuthenticated() bool {
	return e.DMARC == "pass" || e.DKIM == "pass"
}

// FetchReceived retrieves a received email: one API call for its metadata and the
// short-lived raw download URL, one GET for the message. Needs a full-access API key;
// a sending-only key fails with ErrResendRestrictedKey.
func FetchReceived(ctx context.Context, apiKey, emailID string) (ReceivedEmail, error) {
	var meta struct {
		From           string `json:"from"`
		Authentication *struct {
			SPF   string `json:"spf"`
			DKIM  string `json:"dkim"`
			DMARC string `json:"dmarc"`
		} `json:"authentication"`
		Raw *struct {
			DownloadURL string `json:"download_url"`
		} `json:"raw"`
	}
	if err := resendAPI(ctx, apiKey, http.MethodGet, "/emails/receiving/"+url.PathEscape(emailID), nil, &meta); err != nil {
		return ReceivedEmail{}, fmt.Errorf("resend inbound: retrieve email: %w", err)
	}
	if meta.Raw == nil || meta.Raw.DownloadURL == "" {
		return ReceivedEmail{}, errors.New("resend inbound: retrieve email: no raw download url")
	}
	got := ReceivedEmail{From: meta.From}
	if a := meta.Authentication; a != nil {
		got.SPF, got.DKIM, got.DMARC = a.SPF, a.DKIM, a.DMARC
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.Raw.DownloadURL, nil)
	if err != nil {
		return ReceivedEmail{}, err
	}
	resp, err := (&http.Client{Timeout: resendTimeout}).Do(req)
	if err != nil {
		return ReceivedEmail{}, fmt.Errorf("resend inbound: download raw email: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReceivedEmail{}, fmt.Errorf("resend inbound: download raw email: status %d", resp.StatusCode)
	}
	if got.Raw, err = io.ReadAll(io.LimitReader(resp.Body, maxRawEmail)); err != nil {
		return ReceivedEmail{}, fmt.Errorf("resend inbound: download raw email: %w", err)
	}
	return got, nil
}
