package server

import (
	"reflect"
	"testing"
)

// #238: env vars that are set but shadowed by the database were ignored silently.
func TestShadowedEnv(t *testing.T) {
	cases := []struct {
		name    string
		entries []envEntry
		want    []string
	}{
		{
			name:    "nothing set",
			entries: []envEntry{{"EMAIL_SMTP_HOST", "", "smtp.example.com"}},
		},
		{
			name:    "set and agreed",
			entries: []envEntry{{"EMAIL_SMTP_HOST", "smtp.example.com", "smtp.example.com"}},
		},
		{
			name:    "set and differs",
			entries: []envEntry{{"EMAIL_SMTP_HOST", "new.example.com", "old.example.com"}},
			want:    []string{"EMAIL_SMTP_HOST"},
		},
		{
			name: "mixed",
			entries: []envEntry{
				{"EMAIL_SMTP_HOST", "new.example.com", "old.example.com"},
				{"EMAIL_SMTP_USER", "", "user"},
				{"GOOGLE_CLIENT_ID", "same", "same"},
				{"EMAIL_SMTP_PASS", "secret", ""},
			},
			want: []string{"EMAIL_SMTP_HOST", "EMAIL_SMTP_PASS"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shadowedEnv(tc.entries)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("shadowedEnv = %v; want %v", got, tc.want)
			}
		})
	}
}
