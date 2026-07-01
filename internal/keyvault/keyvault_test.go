package keyvault_test

import (
	"database/sql"
	"encoding/hex"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/keyvault"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://file::memory:?cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestOpen_FreshInstall(t *testing.T) {
	database := newTestDB(t)
	const secret = "my-platform-secret"

	v1, err := keyvault.Open(database, secret, "", false)
	if err != nil {
		t.Fatalf("Open (fresh): %v", err)
	}
	dek1 := v1.DEK()

	// Reopening with the same secret must return the same DEK.
	v2, err := keyvault.Open(database, secret, "", false)
	if err != nil {
		t.Fatalf("Open (reopen): %v", err)
	}
	if v2.DEK() != dek1 {
		t.Error("DEK changed between Open calls — keystore not persisted")
	}
}

func TestOpen_WrongSecret(t *testing.T) {
	database := newTestDB(t)
	if _, err := keyvault.Open(database, "correct-secret", "", false); err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if _, err := keyvault.Open(database, "wrong-secret", "", false); err == nil {
		t.Error("expected error opening with wrong secret, got nil")
	}
}

func TestOpen_DevModeNoSecret(t *testing.T) {
	database := newTestDB(t)
	v, err := keyvault.Open(database, "", "", true)
	if err != nil {
		t.Fatalf("dev mode with no secret: %v", err)
	}
	if v.DEK() == ([32]byte{}) {
		t.Error("ephemeral DEK is all-zero")
	}
}

func TestOpen_ProdModeNoSecret(t *testing.T) {
	database := newTestDB(t)
	if _, err := keyvault.Open(database, "", "", false); err == nil {
		t.Error("expected error in prod mode with no secret, got nil")
	}
}

func TestOpen_LegacyMigration(t *testing.T) {
	database := newTestDB(t)

	// Simulate a legacy deployment: write a non-empty *_enc column so the vault
	// detects existing data. We insert into server_settings (always present after
	// migration) with a fake encrypted value.
	_, err := database.Exec(`
		UPDATE server_settings SET smtp_pass_enc = 'fake-legacy-ciphertext' WHERE id = 1`)
	if err != nil {
		t.Fatalf("seed legacy enc data: %v", err)
	}

	// The legacy platform secret was a 64-hex raw key (the old CALNODE_ENCRYPTION_KEY).
	rawKey := make([]byte, 32)
	for i := range rawKey {
		rawKey[i] = byte(i + 1) // deterministic non-zero key
	}
	legacySecret := hex.EncodeToString(rawKey)

	v, err := keyvault.Open(database, legacySecret, "", false)
	if err != nil {
		t.Fatalf("Open (legacy migration): %v", err)
	}

	// DEK must equal the old raw key so existing ciphertext keeps working.
	var expectedDEK [32]byte
	copy(expectedDEK[:], rawKey)
	if v.DEK() != expectedDEK {
		t.Errorf("legacy DEK = %x; want %x", v.DEK(), expectedDEK)
	}

	// Re-opening must return the same DEK without entering migration again.
	v2, err := keyvault.Open(database, legacySecret, "", false)
	if err != nil {
		t.Fatalf("Open after legacy migration: %v", err)
	}
	if v2.DEK() != expectedDEK {
		t.Error("DEK changed on second open after legacy migration")
	}
}

func TestRotatePrimary(t *testing.T) {
	database := newTestDB(t)
	const (
		oldSecret = "old-platform-secret"
		newSecret = "new-platform-secret"
	)

	v1, err := keyvault.Open(database, oldSecret, "", false)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	origDEK := v1.DEK()

	if err := keyvault.RotatePrimary(database, oldSecret, newSecret); err != nil {
		t.Fatalf("RotatePrimary: %v", err)
	}

	// Old secret must now fail.
	if _, err := keyvault.Open(database, oldSecret, "", false); err == nil {
		t.Error("expected failure with old secret after rotation, got nil")
	}

	// New secret must succeed and return the same DEK (data columns untouched).
	v2, err := keyvault.Open(database, newSecret, "", false)
	if err != nil {
		t.Fatalf("Open with new secret: %v", err)
	}
	if v2.DEK() != origDEK {
		t.Error("DEK changed after rotation — data columns would be permanently unreadable")
	}
}

func TestRecoverPrimary(t *testing.T) {
	database := newTestDB(t)
	const (
		platformSecret = "platform-secret"
		recoverySecret = "recovery-secret"
		newSecret      = "new-platform-secret"
	)

	v1, err := keyvault.Open(database, platformSecret, recoverySecret, false)
	if err != nil {
		t.Fatalf("initial Open with recovery secret: %v", err)
	}
	origDEK := v1.DEK()

	// Simulate losing the platform secret: recovery via the recovery KEK.
	if err := keyvault.RecoverPrimary(database, recoverySecret, newSecret); err != nil {
		t.Fatalf("RecoverPrimary: %v", err)
	}

	// New platform secret must open with the same DEK.
	v2, err := keyvault.Open(database, newSecret, "", false)
	if err != nil {
		t.Fatalf("Open after recovery: %v", err)
	}
	if v2.DEK() != origDEK {
		t.Error("DEK changed after recovery — data columns would be permanently unreadable")
	}
}

func TestRecoverPrimary_NoRecoveryRow(t *testing.T) {
	database := newTestDB(t)
	// Open without a recovery secret → no 'recovery' keystore row.
	if _, err := keyvault.Open(database, "platform-secret", "", false); err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := keyvault.RecoverPrimary(database, "recovery-secret", "new"); err == nil {
		t.Error("expected error recovering without a recovery row, got nil")
	}
}
