package handler

import (
	"encoding/json"
	"errors"

	"github.com/calnode/calnode/internal/i18n"
)

// nullableInt distinguishes omission (keep) from explicit null (reset).
type nullableInt struct {
	Present bool
	Value   *int
}

func (n *nullableInt) UnmarshalJSON(data []byte) error {
	n.Present = true
	return json.Unmarshal(data, &n.Value)
}

// durationPolicy applies only to staff-issued invitations. Ordinary bookings
// continue to use the event type's default duration.
type durationPolicy struct {
	Min       *int
	Max       *int
	Increment *int
}

func (p durationPolicy) validate(duration int, loc *i18n.Locale) error {
	if p.Min == nil && p.Max == nil && p.Increment == nil {
		return nil // unconfigured event types are fixed-duration
	}
	if p.Min == nil || p.Max == nil || p.Increment == nil || *p.Min <= 0 ||
		*p.Max < *p.Min || *p.Increment <= 0 {
		return errors.New(loc.T("err_duration_policy_limits"))
	}
	if duration < *p.Min || duration > *p.Max || (duration-*p.Min)%*p.Increment != 0 {
		return errors.New(loc.T("err_duration_policy_range"))
	}
	return nil
}

func (p durationPolicy) allows(duration, defaultDuration int, loc *i18n.Locale) error {
	if duration <= 0 {
		return errors.New(loc.T("err_duration_positive"))
	}
	if p.Min == nil && p.Max == nil && p.Increment == nil && duration != defaultDuration {
		return errors.New(loc.T("err_duration_fixed"))
	}
	return p.validate(duration, loc)
}
