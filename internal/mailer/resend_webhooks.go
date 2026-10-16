package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
)

// resendAPIBase is Resend's REST API. A var so tests can point it at a stub.
var resendAPIBase = "https://api.resend.com"

// ErrResendRestrictedKey means the API key is a sending-only key. RSVP tracking reads
// received emails and manages webhooks, which Resend reserves for full-access keys.
var ErrResendRestrictedKey = errors.New("resend: this API key can only send email; RSVP tracking needs a full-access key")

// resendAPI makes one authenticated JSON call to the Resend API, decoding the response
// into out (when non-nil). Non-2xx answers become errors carrying Resend's own message;
// a sending-only key is reported as ErrResendRestrictedKey so callers can explain it.
func resendAPI(ctx context.Context, apiKey, method, path string, body, out any) error {
	if apiKey == "" {
		return errors.New("resend: api key not configured")
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, resendAPIBase+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: resendTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var apiErr struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		if apiErr.Name == "restricted_api_key" && resp.StatusCode == http.StatusUnauthorized {
			return ErrResendRestrictedKey
		}
		if apiErr.Message != "" {
			return fmt.Errorf("resend api %d: %s", resp.StatusCode, apiErr.Message)
		}
		return fmt.Errorf("resend api %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// inboundWebhookEvent is the only event RSVP tracking subscribes to.
const inboundWebhookEvent = "email.received"

// EnsureResendInboundWebhook makes sure Resend delivers email.received events to endpoint
// and returns that webhook's signing secret. An existing webhook for the same endpoint is
// reused (subscribed to email.received and enabled if it was not), so running this again
// never piles up duplicates or rotates a secret that is already stored.
func EnsureResendInboundWebhook(ctx context.Context, apiKey, endpoint string) (secret string, created bool, err error) {
	var list struct {
		Data []struct {
			ID       string   `json:"id"`
			Endpoint string   `json:"endpoint"`
			Status   string   `json:"status"`
			Events   []string `json:"events"`
		} `json:"data"`
	}
	if err := resendAPI(ctx, apiKey, http.MethodGet, "/webhooks", nil, &list); err != nil {
		return "", false, fmt.Errorf("list webhooks: %w", err)
	}

	var got struct {
		ID            string `json:"id"`
		SigningSecret string `json:"signing_secret"`
	}
	for _, wh := range list.Data {
		if wh.Endpoint != endpoint {
			continue
		}
		if !slices.Contains(wh.Events, inboundWebhookEvent) || wh.Status != "enabled" {
			events := wh.Events
			if !slices.Contains(events, inboundWebhookEvent) {
				events = append(slices.Clone(events), inboundWebhookEvent)
			}
			update := map[string]any{"events": events, "status": "enabled"}
			if err := resendAPI(ctx, apiKey, http.MethodPatch, "/webhooks/"+url.PathEscape(wh.ID), update, nil); err != nil {
				return "", false, fmt.Errorf("update webhook: %w", err)
			}
		}
		if err := resendAPI(ctx, apiKey, http.MethodGet, "/webhooks/"+url.PathEscape(wh.ID), nil, &got); err != nil {
			return "", false, fmt.Errorf("retrieve webhook: %w", err)
		}
		if got.SigningSecret == "" {
			return "", false, errors.New("retrieve webhook: no signing secret in response")
		}
		return got.SigningSecret, false, nil
	}

	create := map[string]any{"endpoint": endpoint, "events": []string{inboundWebhookEvent}}
	if err := resendAPI(ctx, apiKey, http.MethodPost, "/webhooks", create, &got); err != nil {
		return "", false, fmt.Errorf("create webhook: %w", err)
	}
	if got.SigningSecret == "" {
		return "", false, errors.New("create webhook: no signing secret in response")
	}
	return got.SigningSecret, true, nil
}
