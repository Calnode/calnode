package handler

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/calnode/calnode/internal/netutil"
)

// validateBYOServerURL validates an admin-supplied "bring your own server" URL — the
// CalDAV server, the BYO-LLM endpoint, and the LiveKit URL all funnel through here so
// they can't drift apart (they did: three inline copies with three different scheme
// checks, three different parse-error behaviours, and two different metadata-check
// strictnesses). It requires a well-formed URL whose scheme is in allowedSchemes and
// whose host is non-empty, then does a best-effort cloud-metadata check.
//
// Best-effort is deliberate and shared: private/loopback/RFC1918 destinations are
// ALLOWED (self-hosting on your own network is the whole point of these fields), only
// the cloud-metadata range is universally illegitimate, and a transient save-time DNS
// failure is tolerated (returns nil) because the real enforcement is the runtime
// netutil.MetadataSafeTransport that re-checks on every actual dial. See
// netutil.CheckHostnameNotMetadata.
//
// label names the field in the returned error messages ("endpoint", "server URL") so
// each caller reads naturally; callers map the error to their own writeError.
func validateBYOServerURL(ctx context.Context, raw, label string, allowedSchemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || !slices.Contains(allowedSchemes, u.Scheme) {
		return fmt.Errorf("%s must be a valid URL using one of: %s", label, strings.Join(allowedSchemes, ", "))
	}
	if err := netutil.CheckHostnameNotMetadata(ctx, u.Hostname()); err != nil {
		return fmt.Errorf("%s must not resolve to a cloud metadata address", label)
	}
	return nil
}
