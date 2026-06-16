package booking

import (
	"errors"
	"time"
)

var (
	ErrDoubleBooked        = errors.New("booking: time slot is no longer available")
	ErrNotFound            = errors.New("booking: not found")
	ErrAlreadyCancelled    = errors.New("booking: already cancelled")
	ErrTokenNotFound       = errors.New("booking: manage token not found or expired")
	ErrBookingLimitReached = errors.New("booking: active booking limit reached for this invitee")
)

// Booking is a confirmed or cancelled appointment.
type Booking struct {
	ID                 string
	EventTypeID        string
	HostID             string
	StartAt            time.Time
	EndAt              time.Time
	Status             string
	CancellationReason string
	LocationValue      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Attendee is a participant in a booking (the person who made the booking).
type Attendee struct {
	Name         string
	Email        string
	IANATimezone string
}

// Answer is a booker's response to a custom event-type question.
type Answer struct {
	QuestionID string
	Value      string
}

// CreateParams is the input to Service.Create.
type CreateParams struct {
	EventTypeID string
	// HostIDs are the candidate hosts. For RoutingMode "round_robin" Create picks
	// ONE free candidate (least-loaded for this event type; the slice order breaks
	// ties). For any other mode every candidate must be free and host_id is set to
	// the first. For Phase A there is a single candidate for fixed.
	HostIDs       []string
	RoutingMode   string
	StartAt       time.Time
	EndAt         time.Time
	LocationValue string
	Organizer     Attendee
	Answers       []Answer
	// MaxActivePerInvitee caps how many active (upcoming, non-cancelled) bookings
	// the organizer's email may already hold for this event type. 0 = unlimited.
	MaxActivePerInvitee int
}
