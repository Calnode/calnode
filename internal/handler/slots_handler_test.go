package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseDateRange_zeroMaxFuture_defaultsToOneYear(t *testing.T) {
	// MaxFutureDays=0 must be treated as 365, not 0, so omitting to= gives a
	// full-year window rather than collapsing to today.
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/x/slots", nil)

	from, to, ok := parseDateRange(req, now, 0)
	if !ok {
		t.Fatal("parseDateRange returned ok=false; want ok=true")
	}
	if !from.Equal(now) {
		t.Errorf("from = %v; want %v (today)", from, now)
	}
	wantTo := now.AddDate(0, 0, 365)
	if !to.Equal(wantTo) {
		t.Errorf("to = %v; want %v (today+365)", to, wantTo)
	}
}

func TestParseDateRange_farFutureToParam_clampedToEffectiveCap(t *testing.T) {
	// Caller-supplied to=9999-12-31 must be clamped to prevent CPU DoS.
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/x/slots?to=9999-12-31", nil)

	_, to, ok := parseDateRange(req, now, 60)
	if !ok {
		t.Fatal("parseDateRange returned ok=false")
	}
	wantCap := now.AddDate(0, 0, 60)
	if to.After(wantCap) {
		t.Errorf("to = %v; want <= cap %v", to, wantCap)
	}
}

func TestParseDateRange_farFutureToParam_zeroMax_clampedToOneYear(t *testing.T) {
	// With MaxFutureDays=0 (unlimited), far-future to= is still clamped to 365.
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/x/slots?to=9999-12-31", nil)

	_, to, ok := parseDateRange(req, now, 0)
	if !ok {
		t.Fatal("parseDateRange returned ok=false")
	}
	wantCap := now.AddDate(0, 0, 365)
	if to.After(wantCap) {
		t.Errorf("to = %v; want <= 1-year cap %v", to, wantCap)
	}
}

func TestParseDateRange_validExplicitRange(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/x/slots?from=2026-06-16&to=2026-06-18", nil)

	from, to, ok := parseDateRange(req, now, 60)
	if !ok {
		t.Fatal("parseDateRange returned ok=false for valid range")
	}
	wantFrom := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	if !from.Equal(wantFrom) {
		t.Errorf("from = %v; want %v", from, wantFrom)
	}
	if !to.Equal(wantTo) {
		t.Errorf("to = %v; want %v", to, wantTo)
	}
}

func TestParseDateRange_toBeforeFrom_returnsNotOk(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/x/slots?from=2026-06-18&to=2026-06-16", nil)

	_, _, ok := parseDateRange(req, now, 60)
	if ok {
		t.Error("parseDateRange returned ok=true for to < from; want ok=false")
	}
}

func TestParseDateRange_malformedDate_returnsNotOk(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/x/slots?from=not-a-date", nil)

	_, _, ok := parseDateRange(req, now, 60)
	if ok {
		t.Error("parseDateRange returned ok=true for malformed date; want ok=false")
	}
}
