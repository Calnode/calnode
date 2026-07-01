// Package oauthstore holds small pieces shared by every OAuth-based calendar/meeting
// integration (Google Calendar, Microsoft Graph, Zoom) so their token-refresh
// persistence doesn't get re-derived from scratch per provider.
package oauthstore

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/oauth2"
)

// SaveFunc persists a refreshed token. Providers close over their own identifiers
// (user ID, plus calendar ID / account email where relevant) and call their own
// saveToken/saveConnection.
type SaveFunc func(ctx context.Context, tok *oauth2.Token) error

// SavingTokenSource wraps an oauth2.TokenSource and calls Save whenever the access
// token actually changes (i.e. after a refresh) — every provider needs exactly this,
// differing only in what Save does with the new token.
type SavingTokenSource struct {
	Inner  oauth2.TokenSource
	Save   SaveFunc
	Logger *slog.Logger
	LogMsg string // e.g. "gcal: failed to persist refreshed token"
	UserID string // log context only

	last string
}

func (s *SavingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.Inner.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != s.last {
		s.last = tok.AccessToken
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Save(ctx, tok); err != nil {
			s.Logger.Error(s.LogMsg, "error", err, "user_id", s.UserID)
		}
	}
	return tok, nil
}
