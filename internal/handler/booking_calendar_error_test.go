package handler

import (
	"errors"
	"fmt"
	"testing"
)

func TestAssistantCalendarErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errSlotUnavailable, "that time was just taken — please choose another slot"},
		{fmt.Errorf("%w: %w", errCalendarUnavailable, errors.New("private provider diagnostic")), "calendar availability could not be checked; please try again shortly"},
	} {
		if got := assistantBookError(tc.err); got != tc.want {
			t.Fatalf("message = %q, want %q", got, tc.want)
		}
	}
}
