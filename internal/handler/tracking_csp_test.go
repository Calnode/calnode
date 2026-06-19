package handler

import (
	"strings"
	"testing"
)

func TestPublicCSP_strictWhenNoInjection(t *testing.T) {
	if got := publicCSP(trackingSettings{}); got != strictPublicCSP {
		t.Errorf("no code injection must keep the strict CSP; got %q", got)
	}
	// dataLayer alone (inline) also needs nothing external.
	if got := publicCSP(trackingSettings{DataLayerEnabled: true}); got != strictPublicCSP {
		t.Errorf("dataLayer-only must keep the strict CSP; got %q", got)
	}
}

func TestPublicCSP_broadHttpsWhenHeadSet(t *testing.T) {
	got := publicCSP(trackingSettings{HeadHTML: "<script>gtm</script>"})
	if !strings.Contains(got, "script-src 'self' 'unsafe-inline' https:") {
		t.Errorf("expected broad https script-src; got %q", got)
	}
	if !strings.Contains(got, "connect-src 'self' https:") {
		t.Errorf("expected broad https connect-src; got %q", got)
	}
	if !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("must still forbid framing; got %q", got)
	}
}

func TestPublicCSP_allowlistTightens(t *testing.T) {
	got := publicCSP(trackingSettings{
		HeadHTML: "<script>gtm</script>",
		CSPAllow: "https://www.googletagmanager.com https://*.google-analytics.com",
	})
	if !strings.Contains(got, "https://www.googletagmanager.com") {
		t.Errorf("allowlisted origin missing; got %q", got)
	}
	if strings.Contains(got, "'unsafe-inline' https:;") {
		t.Errorf("broad https: should be replaced by the allowlist; got %q", got)
	}
}
