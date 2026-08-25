package mailer

import (
    "context"
	"fmt"
    "net/smtp"
)

// Attachment is a file attached to an outbound email.
type Attachment struct {
	Filename    string
	ContentType string // full MIME type, e.g. `text/calendar; charset=utf-8; method=REQUEST`
	Content     []byte
}

// Message is an outbound email.
type Message struct {
	To          []string
	Subject     string
	Text        string // plain-text body (always set; used as the fallback alternative)
	HTML        string // optional HTML body; when set the message is multipart/alternative
	Attachments []Attachment
}

// Mailer sends email messages.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Noop silently discards all messages. Used when SMTP is not configured.
type Noop struct{}

func (n *Noop) Send(_ context.Context, _ Message) error { return nil }


type loginAuth struct {
	username, password string
}

func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username, password}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
    // This sends: AUTH LOGIN
    // The server will then prompt for the username, which your Next() method will catch.
    return "LOGIN", nil, nil 
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:", "VXNlcm5hbWU6":
			return []byte(a.username), nil
		case "Password:", "UGFzc3dvcmQ6":
			return []byte(a.password), nil
		default:
			return nil, fmt.Errorf("unknown challenge: %s", string(fromServer))
		}
	}
	return nil, nil
}