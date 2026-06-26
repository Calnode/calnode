// Package stt provides speech-to-text for meeting recordings. The only backend today is
// Deepgram, which transcribes a pre-recorded file directly from a URL (we hand it a short-lived
// presigned S3 URL), so Calnode never downloads the media itself.
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Segment is one diarized utterance — a speaker label (0,1,…; Deepgram diarization doesn't know
// names) plus its time window and text.
type Segment struct {
	Speaker int     `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}

// Result is a finished transcription: the full flat text plus per-utterance segments.
type Result struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
}

// Deepgram transcribes via the Deepgram pre-recorded API.
type Deepgram struct {
	apiKey string
	hc     *http.Client
}

// NewDeepgram builds a client for the given API key.
func NewDeepgram(apiKey string) *Deepgram {
	return &Deepgram{apiKey: apiKey, hc: &http.Client{Timeout: 10 * time.Minute}}
}

const deepgramURL = "https://api.deepgram.com/v1/listen?model=nova-2&smart_format=true&punctuate=true&diarize=true&utterances=true"

// TranscribeURL asks Deepgram to fetch + transcribe the audio/video at audioURL (a presigned S3
// GET). Returns the flat transcript + diarized segments.
func (d *Deepgram) TranscribeURL(ctx context.Context, audioURL string) (*Result, error) {
	if d.apiKey == "" {
		return nil, fmt.Errorf("stt: deepgram api key not set")
	}
	body, _ := json.Marshal(map[string]string{"url": audioURL})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepgramURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+d.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stt: deepgram request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return nil, fmt.Errorf("stt: deepgram status %d: %s", resp.StatusCode, strings.TrimSpace(b.String()))
	}
	return parseDeepgram(resp.Body)
}

// parseDeepgram is split out so it's unit-testable against a captured payload.
func parseDeepgram(r io.Reader) (*Result, error) {
	var dg struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string `json:"transcript"`
				} `json:"alternatives"`
			} `json:"channels"`
			Utterances []struct {
				Start      float64 `json:"start"`
				End        float64 `json:"end"`
				Transcript string  `json:"transcript"`
				Speaker    int     `json:"speaker"`
			} `json:"utterances"`
		} `json:"results"`
	}
	if err := json.NewDecoder(r).Decode(&dg); err != nil {
		return nil, fmt.Errorf("stt: decode deepgram response: %w", err)
	}
	out := &Result{}
	for _, u := range dg.Results.Utterances {
		t := strings.TrimSpace(u.Transcript)
		if t == "" {
			continue
		}
		out.Segments = append(out.Segments, Segment{Speaker: u.Speaker, Start: u.Start, End: u.End, Text: t})
	}
	if len(dg.Results.Channels) > 0 && len(dg.Results.Channels[0].Alternatives) > 0 {
		out.Text = strings.TrimSpace(dg.Results.Channels[0].Alternatives[0].Transcript)
	}
	if out.Text == "" { // fall back to joining the utterances
		parts := make([]string, 0, len(out.Segments))
		for _, s := range out.Segments {
			parts = append(parts, s.Text)
		}
		out.Text = strings.Join(parts, " ")
	}
	return out, nil
}
