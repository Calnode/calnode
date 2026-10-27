package keyvault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveExplicitSecretWinsAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "calnode.db")

	secret, source, path, err := ResolvePlatformSecret("explicit-value", dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "explicit-value" {
		t.Fatalf("secret = %q, want the explicit value", secret)
	}
	if source != SecretFromEnv {
		t.Fatalf("source = %q, want %q", source, SecretFromEnv)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty when the env var is used", path)
	}
	if _, statErr := os.Stat(filepath.Join(dir, KeyFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("a key file was written even though CALNODE_ENCRYPTION_KEY was set")
	}
}

func TestResolveGeneratesAndPersistsBesideDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "calnode.db")

	secret, source, path, err := ResolvePlatformSecret("", dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != SecretFromGenerated {
		t.Fatalf("source = %q, want %q", source, SecretFromGenerated)
	}
	if want := filepath.Join(dir, KeyFileName); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if len(secret) != 64 {
		t.Fatalf("secret length = %d, want 64 hex chars (32 bytes)", len(secret))
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("key file missing: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode = %o, want 600: the platform secret must not be group/world readable", perm)
	}
}

func TestResolveIsStableAcrossRestarts(t *testing.T) {
	// This is the whole point: a restart must find the same secret, or every
	// encrypted column in the database becomes unreadable.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "calnode.db")

	first, _, _, err := ResolvePlatformSecret("", dbPath)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, source, _, err := ResolvePlatformSecret("", dbPath)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Fatalf("secret changed between starts: %q then %q", first, second)
	}
	if source != SecretFromFile {
		t.Fatalf("source on second call = %q, want %q", source, SecretFromFile)
	}
}

func TestResolveEmptyKeyFileIsAnErrorNotANewKey(t *testing.T) {
	// Generating a replacement here would destroy access to the existing data.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "calnode.db")
	if err := os.WriteFile(filepath.Join(dir, KeyFileName), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := ResolvePlatformSecret("", dbPath)
	if err == nil {
		t.Fatal("expected an error for an empty key file, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v, want it to mention the empty key file", err)
	}
}

func TestResolveReadsExistingKeyFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "calnode.db")
	if err := os.WriteFile(filepath.Join(dir, KeyFileName), []byte("deadbeef\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	secret, source, _, err := ResolvePlatformSecret("", dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "deadbeef" {
		t.Fatalf("secret = %q, want deadbeef (trailing newline trimmed)", secret)
	}
	if source != SecretFromFile {
		t.Fatalf("source = %q, want %q", source, SecretFromFile)
	}
}

func TestResolveNeedsADatabasePath(t *testing.T) {
	if _, _, _, err := ResolvePlatformSecret("", ""); err == nil {
		t.Fatal("expected an error when there is no database path")
	}
}

func TestResolveCreatesMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "deeper", "calnode.db")

	_, _, path, err := ResolvePlatformSecret("", dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("key file not created in missing directory: %v", statErr)
	}
}
