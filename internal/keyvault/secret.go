// Package keyvault: platform-secret resolution.
//
// The vault needs one value from outside the database: the platform secret that
// derives the KEK. Until now that value had to come from the environment, and a
// missing value was handled by guessing intent from the BASE_URL scheme:
//
//	BASE_URL http://...  -> devMode -> ephemeral DEK, warned, secrets lost on restart
//	BASE_URL https://... -> fatal error, server will not start
//
// Both arms are wrong for a plain `docker run`, which is the documented first
// install. Over http it silently generates an ephemeral DEK, so the SMTP
// password, Stripe keys and OAuth client secrets stored in the database become
// unreadable at the next restart. Over https it refuses to boot at all. And
// because the scheme is the only signal, a production deployment behind a TLS
// terminating proxy (BASE_URL http, TLS at the edge) takes the silent arm.
//
// Persisting a generated secret next to the database removes the guess. The key
// file lives beside the database it unlocks, which is also where backups already
// go, so the pair stays together. An explicit CALNODE_ENCRYPTION_KEY still wins,
// which is what the NS8 module and any externally managed deployment rely on.

//nolint:revive // file-level rationale.
package keyvault

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// KeyFileName is the persisted platform secret written next to the database.
//
// #nosec G101 -- this is a file NAME, not a credential. The secret it names is
// generated at runtime and never appears in source.
const KeyFileName = "calnode.key"

// Where the platform secret came from, for logging and for tests.
const (
	// #nosec G101 -- the NAME of an environment variable, not a credential.
	SecretFromEnv       = "CALNODE_ENCRYPTION_KEY"
	SecretFromFile      = "file"
	SecretFromGenerated = "generated"
)

// ResolvePlatformSecret returns the platform secret that unlocks the vault.
//
// Precedence:
//  1. explicit (CALNODE_ENCRYPTION_KEY) - used verbatim, nothing is written.
//  2. an existing key file beside the database - read and reused.
//  3. no key at all - a new random secret is generated, written 0600 beside the
//     database, and returned. This is the plain `docker run` case.
//
// dbPath is the on-disk database file; the key is placed in the same directory.
// The purpose is that a restart, an update, or a restored volume finds the same
// secret and the ciphertext in the database stays readable.
func ResolvePlatformSecret(explicit, dbPath string) (secret, source, path string, err error) {
	if explicit != "" {
		return explicit, SecretFromEnv, "", nil
	}
	if strings.TrimSpace(dbPath) == "" {
		return "", "", "", errors.New("keyvault: no database path to place " + KeyFileName + " beside")
	}

	path = filepath.Join(filepath.Dir(dbPath), KeyFileName)

	// An unreadable-but-present file must not silently fall through to
	// generating a new secret: that would replace a working key with one that
	// cannot decrypt the existing database.
	// #nosec G304 -- path is derived from the operator's DATABASE_URL, not from
	// request data. Where the key lives is deployment configuration.
	if raw, readErr := os.ReadFile(path); readErr == nil {
		existing := strings.TrimSpace(string(raw))
		if existing == "" {
			return "", "", "", fmt.Errorf(
				"keyvault: %s exists but is empty; delete it to generate a new key, "+
					"or restore it together with the database", path)
		}
		return existing, SecretFromFile, path, nil
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return "", "", "", fmt.Errorf("keyvault: read %s: %w", path, readErr)
	}

	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return "", "", "", fmt.Errorf("keyvault: create key directory: %w", mkErr)
	}

	var raw [32]byte
	if _, randErr := rand.Read(raw[:]); randErr != nil {
		return "", "", "", fmt.Errorf("keyvault: generate platform secret: %w", randErr)
	}
	generated := hex.EncodeToString(raw[:])

	// O_EXCL so two processes starting at once cannot overwrite each other; the
	// loser re-reads the winner's file rather than proceeding with its own.
	// #nosec G304 -- same operator-controlled path as above.
	f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(createErr, fs.ErrExist) {
		// #nosec G304 -- same operator-controlled path.
		if existing, readErr := os.ReadFile(path); readErr == nil {
			if s := strings.TrimSpace(string(existing)); s != "" {
				return s, SecretFromFile, path, nil
			}
		}
		return "", "", "", fmt.Errorf("keyvault: %s appeared but could not be read", path)
	}
	if createErr != nil {
		return "", "", "", fmt.Errorf("keyvault: create %s: %w", path, createErr)
	}
	if _, writeErr := f.WriteString(generated + "\n"); writeErr != nil {
		// Join rather than discard: a close or cleanup failure is worth reporting
		// alongside the write failure, and a half-written key file must not be
		// left behind looking usable.
		writeErr = errors.Join(writeErr, f.Close())
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			writeErr = errors.Join(writeErr, rmErr)
		}
		return "", "", "", fmt.Errorf("keyvault: write %s: %w", path, writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			closeErr = errors.Join(closeErr, rmErr)
		}
		return "", "", "", fmt.Errorf("keyvault: close %s: %w", path, closeErr)
	}

	return generated, SecretFromGenerated, path, nil
}
