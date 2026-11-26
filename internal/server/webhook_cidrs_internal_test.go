package server

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"
)

func TestParseWebhookAllowedCIDRs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	got := parseWebhookAllowedCIDRs([]string{"10.200.50.0/24", " 172.20.0.5 ", "not-a-cidr", "169.254.0.0/16", ""}, logger)

	// The bad entry is dropped (that network stays blocked), the rest are kept.
	if len(got) != 3 {
		t.Fatalf("parsed %d networks %v; want 3", len(got), got)
	}
	if !got[1].Contains(net.ParseIP("172.20.0.5")) || got[1].Contains(net.ParseIP("172.20.0.6")) {
		t.Errorf("bare address parsed as %v; want a single host", got[1])
	}
	out := logs.String()
	if !strings.Contains(out, `cidrs="[10.200.50.0/24 172.20.0.5/32 169.254.0.0/16]"`) {
		t.Errorf("allowed-networks log should list the parsed networks only:\n%s", out)
	}
	for _, want := range []string{"ignoring unparseable entries", "not-a-cidr", "stay blocked whatever is listed"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs missing %q:\n%s", want, out)
		}
	}

	if got := parseWebhookAllowedCIDRs(nil, logger); len(got) != 0 {
		t.Errorf("empty env parsed as %v; want none", got)
	}
}
