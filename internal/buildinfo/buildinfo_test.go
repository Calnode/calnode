package buildinfo_test

import (
	"testing"

	"github.com/calnode/calnode/internal/buildinfo"
)

func TestGet_defaults(t *testing.T) {
	info := buildinfo.Get()

	// Version defaults to "dev" when not stamped via ldflags.
	if info.Version == "" {
		t.Error("Version is empty; want at least the \"dev\" default")
	}
	// Commit/BuildTime are "unknown" under `go test` (no VCS stamp), never empty.
	if info.Commit == "" {
		t.Error("Commit is empty; want \"unknown\" fallback")
	}
	if info.BuildTime == "" {
		t.Error("BuildTime is empty; want \"unknown\" fallback")
	}
}
