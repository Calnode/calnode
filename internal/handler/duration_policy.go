package handler

import "fmt"

// durationPolicy applies only to staff-issued invitations. Ordinary bookings
// continue to use the event type's default duration.
type durationPolicy struct {
	Min       *int
	Max       *int
	Increment *int
}

func (p durationPolicy) validate(duration int) error {
	if p.Min == nil && p.Max == nil && p.Increment == nil {
		return nil // unconfigured event types are fixed-duration
	}
	if p.Min == nil || p.Max == nil || p.Increment == nil || *p.Min <= 0 ||
		*p.Max < *p.Min || *p.Increment <= 0 {
		return fmt.Errorf("duration limits require positive min/max/increment with max >= min")
	}
	if duration < *p.Min || duration > *p.Max || (duration-*p.Min)%*p.Increment != 0 {
		return fmt.Errorf("duration_minutes must fit the event type's duration range and increment")
	}
	return nil
}

func (p durationPolicy) allows(duration, defaultDuration int) error {
	if duration <= 0 {
		return fmt.Errorf("duration_minutes must be positive")
	}
	if p.Min == nil && p.Max == nil && p.Increment == nil && duration != defaultDuration {
		return fmt.Errorf("this event type has a fixed duration")
	}
	return p.validate(duration)
}
