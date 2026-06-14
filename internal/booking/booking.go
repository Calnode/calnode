package booking

import (
	"errors"
	"time"
)

var (
	ErrDoubleBooked     = errors.New("booking: time slot is no longer available")
	ErrNotFound         = errors.New("booking: not found")
	ErrAlreadyCancelled = errors.New("booking: already cancelled")
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
// HostIDs comes from Slot.HostIDs — single element for fixed/round_robin/priority,
// all host IDs for collective. All hosts are checked for overlap before inserting.
type CreateParams struct {
	EventTypeID   string
	HostIDs       []string
	StartAt       time.Time
	EndAt         time.Time
	LocationValue string
	Organizer     Attendee
	Answers       []Answer
}
