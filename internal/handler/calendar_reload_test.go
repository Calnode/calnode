package handler

import (
	"github.com/calnode/calnode/internal/calendar"
	"testing"
)

type reloadProvider struct {
	calendar.Provider
	name string
}

func (p *reloadProvider) Name() string { return p.name }

func TestSetGoogleCalendarPreservesPrimaryAndSnapshot(t *testing.T) {
	for _, primary := range []string{"google", "microsoft"} {
		for _, clear := range []bool{false, true} {
			t.Run(primary+map[bool]string{false: "/replace", true: "/clear"}[clear], func(t *testing.T) {
				h := &Handler{}
				old := calendar.NewService(nil)
				old.Register(&reloadProvider{name: primary})
				other := "google"
				if primary == "google" {
					other = "microsoft"
				}
				old.Register(&reloadProvider{name: other})
				h.SetCalendar(old)
				replacement := &reloadProvider{name: "google"}
				var provider calendar.Provider = replacement
				if clear {
					provider = nil
				}
				h.setGoogleCalendar(provider)
				current := h.getCal()
				wantPrimary := primary
				if clear && primary == "google" {
					wantPrimary = "microsoft"
				}
				if current.Primary().Name() != wantPrimary {
					t.Fatalf("primary=%s, want %s", current.Primary().Name(), wantPrimary)
				}
				if current.Provider("microsoft") != old.Provider("microsoft") {
					t.Fatal("non-Google provider replaced")
				}
				if clear {
					if current.Provider("google") != nil {
						t.Fatal("Google not removed")
					}
				} else if current.Provider("google") != replacement {
					t.Fatal("Google not replaced")
				}
				if old.Provider("google") == nil || len(old.ProviderNames()) != 2 {
					t.Fatal("old snapshot mutated")
				}
			})
		}
	}
	h := &Handler{}
	h.setGoogleCalendar(&reloadProvider{name: "google"})
	if h.getCal() == nil {
		t.Fatal("first Google configuration not installed")
	}
	h.setGoogleCalendar(nil)
	if h.getCal() != nil {
		t.Fatal("Google-only removal did not clear service")
	}
}
