package main

import (
	"fmt"
	"os"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/keyvault"
)

// runRotateKey is invoked as: calnode rotate-key <new-platform-secret>
//
// It re-wraps the DEK under the new secret without touching any data columns.
// The current secret is read from CALNODE_ENCRYPTION_KEY (env or .env file).
// After a successful rotation, update CALNODE_ENCRYPTION_KEY to <new-platform-secret>
// before the next server start.
func runRotateKey(args []string) {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: calnode rotate-key <new-platform-secret>")
		fmt.Fprintln(os.Stderr, "       old secret is read from CALNODE_ENCRYPTION_KEY")
		os.Exit(1)
	}
	newSecret := args[0]

	oldSecret := os.Getenv("CALNODE_ENCRYPTION_KEY")
	if oldSecret == "" {
		fmt.Fprintln(os.Stderr, "error: CALNODE_ENCRYPTION_KEY is not set")
		os.Exit(1)
	}
	if oldSecret == newSecret {
		fmt.Fprintln(os.Stderr, "error: new secret is identical to the current one")
		os.Exit(1)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "sqlite://./data/calnode.db"
	}

	database, err := db.Open(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := keyvault.RotatePrimary(database, oldSecret, newSecret); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("key rotated — update CALNODE_ENCRYPTION_KEY to the new secret before the next server start")
}
