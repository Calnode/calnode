package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
)

// runResetAdmin is invoked when the binary is called as:
//
//	calnode reset-admin <email> <new-password>
//
// It resets the password for the named user and enables email_login on their
// account. This is the last-resort recovery path when SMTP is unavailable and
// the admin is locked out.
func runResetAdmin(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: calnode reset-admin <email> <new-password>")
		os.Exit(1)
	}
	email := strings.TrimSpace(strings.ToLower(args[0]))
	password := args[1]

	if len(password) < 8 || len(password) > 72 {
		fmt.Fprintln(os.Stderr, "error: password must be 8–72 characters")
		os.Exit(1)
	}

	cfg := config.Load()

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to hash password: %v\n", err)
		os.Exit(1)
	}

	res, err := database.Exec(
		`UPDATE users SET password_hash = ?, email_login = 1 WHERE email = ?`,
		string(hash), email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: database update failed: %v\n", err)
		os.Exit(1)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fmt.Fprintf(os.Stderr, "error: no user found with email %q\n", email)
		os.Exit(1)
	}

	fmt.Printf("password reset for %s — they can now log in with email + new password\n", email)
}
