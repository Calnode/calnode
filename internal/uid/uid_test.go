package uid_test

import (
	"regexp"
	"testing"

	"github.com/calnode/calnode/internal/uid"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNew_format(t *testing.T) {
	id := uid.New()
	if !uuidRE.MatchString(id) {
		t.Errorf("uid.New() = %q; want UUID v4 format", id)
	}
}

func TestNew_unique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id := uid.New()
		if seen[id] {
			t.Fatalf("uid.New() produced duplicate: %q", id)
		}
		seen[id] = true
	}
}
