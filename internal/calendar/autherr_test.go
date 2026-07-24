package calendar

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/oauth2"
)

func TestIsReauthErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"google revoked", errors.New(`oauth2: "invalid_grant" "Token has been expired or revoked."`), true},
		{"ms security interrupt", fmt.Errorf("microsoft: list calendars call: %w",
			errors.New(`oauth2: "invalid_grant" "AADSTS70000: Account security interrupt..."`)), true},
		{"typed retrieve error", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"typed interaction required", &oauth2.RetrieveError{ErrorCode: "interaction_required"}, true},
		{"transient network", errors.New("microsoft: list calendars call: dial tcp: i/o timeout"), false},
		{"http 500", errors.New("gcal: list calendars status 500"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsReauthErr(tc.err); got != tc.want {
				t.Errorf("IsReauthErr(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}
