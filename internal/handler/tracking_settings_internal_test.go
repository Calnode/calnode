package handler

import (
	"strings"
	"testing"
)

func TestTagIDFormats(t *testing.T) {
	gtmOK := []string{"GTM-ABC123", "GTM-T2KZ9P4", "GTM-0"}
	gtmBad := []string{"", "GTM-", "gtm-abc123", "GTM-AB C", "GTM-<script>", "G-12345", "GTM-AB;rm", "javascript:alert(1)"}
	for _, s := range gtmOK {
		if !gtmIDRe.MatchString(s) {
			t.Errorf("gtmIDRe rejected valid %q", s)
		}
	}
	for _, s := range gtmBad {
		if gtmIDRe.MatchString(s) {
			t.Errorf("gtmIDRe accepted INVALID %q (XSS/injection risk)", s)
		}
	}
	ga4OK := []string{"G-ABC1234567", "G-0"}
	ga4Bad := []string{"", "G-", "g-abc", "GA-123", "G-12 34", "G-<x>", "GTM-ABC123"}
	for _, s := range ga4OK {
		if !ga4IDRe.MatchString(s) {
			t.Errorf("ga4IDRe rejected valid %q", s)
		}
	}
	for _, s := range ga4Bad {
		if ga4IDRe.MatchString(s) {
			t.Errorf("ga4IDRe accepted INVALID %q", s)
		}
	}
}

func TestPublicCSP_nativeTag(t *testing.T) {
	// No injection, no tag → strict.
	if got := publicCSP(trackingSettings{}); got != strictPublicCSP {
		t.Errorf("empty config should be strict CSP; got %q", got)
	}
	// A native tag (even with no head_html) relaxes the CSP and allows Google's domains.
	csp := publicCSP(trackingSettings{GA4MeasurementID: "G-ABC1234567"})
	if csp == strictPublicCSP {
		t.Error("a native GA4 tag should relax the CSP")
	}
	if !strings.Contains(csp, "https://www.googletagmanager.com") || !strings.Contains(csp, "google-analytics.com") {
		t.Errorf("native-tag CSP missing Google domains: %q", csp)
	}
	// Even with a narrow custom allowlist, Google's domains are still added (tag not self-blocked).
	csp2 := publicCSP(trackingSettings{GTMContainerID: "GTM-ABC123", CSPAllow: "https://example.com"})
	if !strings.Contains(csp2, "https://www.googletagmanager.com") {
		t.Errorf("custom allowlist should still include Google tag domains: %q", csp2)
	}
}
