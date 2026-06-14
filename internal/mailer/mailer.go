package mailer

import "context"

// Message is an outbound email.
type Message struct {
	To      []string
	Subject string
	Text    string // plain-text body
}

// Mailer sends email messages.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Noop silently discards all messages. Used when SMTP is not configured.
type Noop struct{}

func (n *Noop) Send(_ context.Context, _ Message) error { return nil }
