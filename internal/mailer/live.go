package mailer

import (
	"context"
	"sync"
)

// Live is a Mailer that wraps a hot-swappable inner Mailer. All calls to Send
// are forwarded to whatever the current inner Mailer is at the time of the call,
// so SMTP settings can be changed at runtime without restarting the server.
type Live struct {
	mu      sync.RWMutex
	current Mailer
}

// NewLive creates a Live mailer with the given initial Mailer.
func NewLive(initial Mailer) *Live {
	return &Live{current: initial}
}

// Send forwards to the current inner Mailer.
func (l *Live) Send(ctx context.Context, msg Message) error {
	l.mu.RLock()
	m := l.current
	l.mu.RUnlock()
	return m.Send(ctx, msg)
}

// Swap atomically replaces the inner Mailer.
func (l *Live) Swap(m Mailer) {
	l.mu.Lock()
	l.current = m
	l.mu.Unlock()
}

// IsEnabled reports whether the current inner Mailer is a real sender (not Noop).
func (l *Live) IsEnabled() bool {
	l.mu.RLock()
	_, noop := l.current.(*Noop)
	l.mu.RUnlock()
	return !noop
}
