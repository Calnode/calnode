package calendar

import (
	"errors"
	"strings"

	"golang.org/x/oauth2"
)

// ErrReauthRequired signals that a provider rejected the account's stored OAuth credentials —
// the refresh token was revoked or expired, or the account needs an interactive security check.
// The remedy is user-driven (disconnect and reconnect), so callers should surface it as such
// rather than as a transient provider outage.
var ErrReauthRequired = errors.New("calendar: reconnect required")

// IsReauthErr reports whether err indicates the account's OAuth grant is no longer usable, as
// opposed to a transient failure. It matches both a typed oauth2.RetrieveError and the string
// form providers wrap it in (Google returns "invalid_grant"; Microsoft returns "invalid_grant"
// alongside an AADSTS code).
func IsReauthErr(err error) bool {
	if err == nil {
		return false
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "invalid_grant", "interaction_required", "consent_required", "unauthorized_client", "invalid_client":
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "invalid_grant") ||
		strings.Contains(s, "interaction_required") ||
		strings.Contains(s, "AADSTS70000") || // account security interrupt / compromised
		strings.Contains(s, "AADSTS700082") // refresh token expired due to inactivity
}
