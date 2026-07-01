package oauthstore

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"golang.org/x/oauth2"
)

type fakeTokenSource struct {
	tok *oauth2.Token
	err error
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) { return f.tok, f.err }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSavingTokenSource_savesOnFirstToken(t *testing.T) {
	var saved *oauth2.Token
	s := &SavingTokenSource{
		Inner:  &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a1"}},
		Save:   func(ctx context.Context, tok *oauth2.Token) error { saved = tok; return nil },
		Logger: testLogger(),
	}
	tok, err := s.Token()
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok.AccessToken != "a1" {
		t.Errorf("AccessToken = %q; want a1", tok.AccessToken)
	}
	if saved == nil || saved.AccessToken != "a1" {
		t.Error("Save was not called with the new token")
	}
}

func TestSavingTokenSource_doesNotResaveUnchangedToken(t *testing.T) {
	calls := 0
	inner := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a1"}}
	s := &SavingTokenSource{
		Inner:  inner,
		Save:   func(ctx context.Context, tok *oauth2.Token) error { calls++; return nil },
		Logger: testLogger(),
	}
	if _, err := s.Token(); err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if _, err := s.Token(); err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if calls != 1 {
		t.Errorf("Save called %d times; want 1 (token unchanged on second call)", calls)
	}
}

func TestSavingTokenSource_resavesOnRefresh(t *testing.T) {
	calls := 0
	inner := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a1"}}
	s := &SavingTokenSource{
		Inner:  inner,
		Save:   func(ctx context.Context, tok *oauth2.Token) error { calls++; return nil },
		Logger: testLogger(),
	}
	if _, err := s.Token(); err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	inner.tok = &oauth2.Token{AccessToken: "a2"} // simulate a refresh
	if _, err := s.Token(); err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if calls != 2 {
		t.Errorf("Save called %d times; want 2 (token changed)", calls)
	}
}

func TestSavingTokenSource_saveErrorDoesNotFailToken(t *testing.T) {
	s := &SavingTokenSource{
		Inner:  &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a1"}},
		Save:   func(ctx context.Context, tok *oauth2.Token) error { return errors.New("db down") },
		Logger: testLogger(),
		LogMsg: "test: persist failed",
		UserID: "u1",
	}
	tok, err := s.Token()
	if err != nil {
		t.Fatalf("Token() error = %v; want nil (Save failures are logged, not propagated)", err)
	}
	if tok.AccessToken != "a1" {
		t.Errorf("AccessToken = %q; want a1", tok.AccessToken)
	}
}

func TestSavingTokenSource_innerErrorPropagates(t *testing.T) {
	wantErr := errors.New("refresh failed")
	s := &SavingTokenSource{
		Inner:  &fakeTokenSource{err: wantErr},
		Save:   func(ctx context.Context, tok *oauth2.Token) error { t.Fatal("Save should not be called"); return nil },
		Logger: testLogger(),
	}
	_, err := s.Token()
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want %v", err, wantErr)
	}
}
