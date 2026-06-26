package stt

import (
	"strings"
	"testing"
)

func TestParseDeepgram(t *testing.T) {
	sample := `{"results":{"channels":[{"alternatives":[{"transcript":"Hello there. How are you?"}]}],` +
		`"utterances":[{"start":0.5,"end":1.2,"transcript":"Hello there.","speaker":0},` +
		`{"start":1.5,"end":2.4,"transcript":"How are you?","speaker":1}]}}`
	res, err := parseDeepgram(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Text != "Hello there. How are you?" {
		t.Errorf("text = %q", res.Text)
	}
	if len(res.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(res.Segments))
	}
	if res.Segments[1].Speaker != 1 || res.Segments[1].Text != "How are you?" {
		t.Errorf("seg[1] = %+v", res.Segments[1])
	}
}

func TestParseDeepgram_fallbackText(t *testing.T) {
	// No channel transcript present → Text falls back to the joined utterances.
	sample := `{"results":{"utterances":[{"start":0,"end":1,"transcript":"one","speaker":0},` +
		`{"start":1,"end":2,"transcript":"two","speaker":0}]}}`
	res, err := parseDeepgram(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Text != "one two" {
		t.Errorf("fallback text = %q", res.Text)
	}
}
