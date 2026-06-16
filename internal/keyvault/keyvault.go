// Package keyvault implements envelope encryption for Calnode's secret columns.
//
// Architecture:
//
//	platform secret ──Argon2id(salt,params)──▶ KEK ──AES-GCM──▶ wraps/unwraps DEK
//	recovery secret ──Argon2id(salt,params)──▶ KEK_recovery ──AES-GCM──▶ (same DEK, escrow)
//	DEK ──AES-GCM──▶ all *_enc columns in the database
//
// The DEK is a random 32-byte key generated once on first boot and stored
// wrapped inside the DB (crypto_keystore table), so it travels with
// Litestream backups. The platform secret (CALNODE_ENCRYPTION_KEY) is the
// KEK input and is the only value that must be kept outside the DB.
package keyvault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/argon2"
)

const (
	labelPrimary  = "primary"
	labelRecovery = "recovery"
	kdfArgon2id   = "argon2id"
	saltLen       = 16
	dekLen        = 32
)

type kdfParams struct {
	M uint32 `json:"m"`
	T uint32 `json:"t"`
	P uint8  `json:"p"`
}

// defaultKDFParams are stored alongside the wrapped DEK so they can evolve
// without breaking existing keystores.
var defaultKDFParams = kdfParams{M: 65536, T: 3, P: 2}

// Vault holds the unwrapped DEK. Obtain one via Open; use DEKHex to pass the
// key to the rest of the application.
type Vault struct {
	dek [32]byte
}

// DEK returns the 32-byte data encryption key.
func (v *Vault) DEK() [32]byte { return v.dek }

// DEKHex returns the DEK as a 64-character hex string for callers that expect
// the key in that format (gcal.New, webhook.New, secret.ParseKey).
func (v *Vault) DEKHex() string { return hex.EncodeToString(v.dek[:]) }

// Open is the startup state machine. It must be called after all DB migrations
// have run (the crypto_keystore table must exist).
//
// Behaviour:
//  1. platformSecret set + keystore has 'primary' row → derive KEK, unwrap DEK.
//  2. platformSecret set + keystore empty + no *_enc data → fresh install:
//     generate random DEK, wrap under primary (and recovery) KEK, store.
//  3. platformSecret set + keystore empty + *_enc data present → legacy migration:
//     adopt platformSecret (hex-decoded) as the DEK so existing ciphertext keeps
//     working; wrap and store it.
//  4. platformSecret empty + devMode → ephemeral random DEK; warn; never stored.
//  5. platformSecret empty + !devMode → fatal error.
func Open(db *sql.DB, platformSecret, recoverySecret string, devMode bool) (*Vault, error) {
	if platformSecret == "" {
		if devMode {
			slog.Warn("keyvault: CALNODE_ENCRYPTION_KEY is not set — using an ephemeral key that will change on restart; stored secrets will become unreadable")
			return ephemeralVault()
		}
		return nil, errors.New("keyvault: CALNODE_ENCRYPTION_KEY must be set in production; generate one with: openssl rand -hex 32")
	}

	// Check for an existing primary keystore entry.
	row := db.QueryRow(`
		SELECT wrapped_dek, kdf_salt, kdf_params
		FROM crypto_keystore WHERE label = ?`, labelPrimary)

	var wrappedDEK, kdfSalt []byte
	var paramsJSON string
	err := row.Scan(&wrappedDEK, &kdfSalt, &paramsJSON)

	if err == nil {
		// Normal path: unwrap the existing DEK.
		v, err := openExisting(platformSecret, wrappedDEK, kdfSalt, paramsJSON)
		if err != nil {
			return nil, err
		}
		slog.Info("keyvault: DEK unwrapped from keystore (primary)")
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("keyvault: query primary: %w", err)
	}

	// Keystore is empty. Detect legacy vs. fresh install.
	legacy, err := hasEncryptedData(db)
	if err != nil {
		return nil, fmt.Errorf("keyvault: detect legacy data: %w", err)
	}
	if legacy {
		slog.Info("keyvault: legacy deployment detected — adopting existing key as DEK and wrapping it in keystore")
		return migrateLegacy(db, platformSecret, recoverySecret)
	}
	slog.Info("keyvault: fresh install — generating DEK and storing in keystore")
	return freshInstall(db, platformSecret, recoverySecret)
}

// RotatePrimary re-wraps the DEK under a new platform secret. The data columns
// are never touched. oldSecret must match the current CALNODE_ENCRYPTION_KEY.
func RotatePrimary(db *sql.DB, oldSecret, newSecret string) error {
	row := db.QueryRow(`
		SELECT wrapped_dek, kdf_salt, kdf_params
		FROM crypto_keystore WHERE label = ?`, labelPrimary)
	var wrappedDEK, kdfSalt []byte
	var paramsJSON string
	if err := row.Scan(&wrappedDEK, &kdfSalt, &paramsJSON); err != nil {
		return fmt.Errorf("keyvault rotate: read primary row: %w", err)
	}
	kek, err := deriveKEK(oldSecret, kdfSalt, paramsJSON)
	if err != nil {
		return err
	}
	dek, err := unwrapDEK(kek, wrappedDEK)
	if err != nil {
		return fmt.Errorf("keyvault rotate: old secret is wrong: %w", err)
	}
	return insertKeystoreRow(db, labelPrimary, dek, newSecret)
}

// RecoverPrimary uses the recovery secret (CALNODE_RECOVERY_SECRET) to
// establish a new platform secret when the old one is lost.
func RecoverPrimary(db *sql.DB, recoverySecret, newPlatformSecret string) error {
	row := db.QueryRow(`
		SELECT wrapped_dek, kdf_salt, kdf_params
		FROM crypto_keystore WHERE label = ?`, labelRecovery)
	var wrappedDEK, kdfSalt []byte
	var paramsJSON string
	if err := row.Scan(&wrappedDEK, &kdfSalt, &paramsJSON); err != nil {
		return fmt.Errorf("keyvault recover: read recovery row (was CALNODE_RECOVERY_SECRET set on this instance?): %w", err)
	}
	kek, err := deriveKEK(recoverySecret, kdfSalt, paramsJSON)
	if err != nil {
		return err
	}
	dek, err := unwrapDEK(kek, wrappedDEK)
	if err != nil {
		return fmt.Errorf("keyvault recover: wrong recovery secret: %w", err)
	}
	return insertKeystoreRow(db, labelPrimary, dek, newPlatformSecret)
}

// --- internal state machine helpers ---

func openExisting(platformSecret string, wrappedDEK, kdfSalt []byte, paramsJSON string) (*Vault, error) {
	kek, err := deriveKEK(platformSecret, kdfSalt, paramsJSON)
	if err != nil {
		return nil, err
	}
	dek, err := unwrapDEK(kek, wrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("keyvault: failed to unwrap DEK — is CALNODE_ENCRYPTION_KEY correct? %w", err)
	}
	return &Vault{dek: dek}, nil
}

func freshInstall(db *sql.DB, platformSecret, recoverySecret string) (*Vault, error) {
	var dek [32]byte
	if _, err := rand.Read(dek[:]); err != nil {
		return nil, fmt.Errorf("keyvault: generate DEK: %w", err)
	}
	if err := storeKeystore(db, dek, platformSecret, recoverySecret); err != nil {
		return nil, err
	}
	return &Vault{dek: dek}, nil
}

func migrateLegacy(db *sql.DB, platformSecret, recoverySecret string) (*Vault, error) {
	// The existing deployment used platformSecret directly as the raw AES-256 key.
	// Adopt that value as the DEK so all existing *_enc ciphertext keeps decrypting.
	raw, err := hex.DecodeString(platformSecret)
	if err != nil || len(raw) != dekLen {
		return nil, fmt.Errorf("keyvault: legacy migration: CALNODE_ENCRYPTION_KEY must be a 64-hex-char raw key (the original value); cannot migrate")
	}
	var dek [32]byte
	copy(dek[:], raw)
	if err := storeKeystore(db, dek, platformSecret, recoverySecret); err != nil {
		return nil, err
	}
	return &Vault{dek: dek}, nil
}

func storeKeystore(db *sql.DB, dek [32]byte, platformSecret, recoverySecret string) error {
	if err := insertKeystoreRow(db, labelPrimary, dek, platformSecret); err != nil {
		return err
	}
	if recoverySecret != "" {
		if err := insertKeystoreRow(db, labelRecovery, dek, recoverySecret); err != nil {
			return err
		}
	}
	return nil
}

func insertKeystoreRow(db *sql.DB, label string, dek [32]byte, secret string) error {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("keyvault: generate salt for %s: %w", label, err)
	}
	paramsJSON, _ := json.Marshal(defaultKDFParams)
	kek, err := deriveKEK(secret, salt, string(paramsJSON))
	if err != nil {
		return err
	}
	wrapped, err := wrapDEK(kek, dek)
	if err != nil {
		return fmt.Errorf("keyvault: wrap DEK for %s: %w", label, err)
	}
	_, err = db.Exec(`
		INSERT INTO crypto_keystore
		  (label, wrapped_dek, kdf, kdf_salt, kdf_params, dek_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, datetime('now'), datetime('now'))
		ON CONFLICT(label) DO UPDATE SET
		  wrapped_dek = excluded.wrapped_dek,
		  kdf_salt    = excluded.kdf_salt,
		  kdf_params  = excluded.kdf_params,
		  updated_at  = excluded.updated_at`,
		label, wrapped, kdfArgon2id, salt, string(paramsJSON))
	return err
}

// --- KDF + AES-GCM primitives ---

func deriveKEK(secret string, salt []byte, paramsJSON string) ([32]byte, error) {
	var p kdfParams
	if err := json.Unmarshal([]byte(paramsJSON), &p); err != nil {
		return [32]byte{}, fmt.Errorf("keyvault: parse kdf_params: %w", err)
	}
	raw := argon2.IDKey([]byte(secret), salt, p.T, p.M, p.P, 32)
	var kek [32]byte
	copy(kek[:], raw)
	return kek, nil
}

func wrapDEK(kek, dek [32]byte) ([]byte, error) {
	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, dek[:], nil), nil
}

func unwrapDEK(kek [32]byte, wrapped []byte) ([32]byte, error) {
	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return [32]byte{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return [32]byte{}, err
	}
	if len(wrapped) < gcm.NonceSize() {
		return [32]byte{}, errors.New("wrapped key too short")
	}
	plain, err := gcm.Open(nil, wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():], nil)
	if err != nil {
		return [32]byte{}, err
	}
	if len(plain) != dekLen {
		return [32]byte{}, fmt.Errorf("unexpected DEK length after unwrap: %d", len(plain))
	}
	var dek [32]byte
	copy(dek[:], plain)
	return dek, nil
}

// hasEncryptedData returns true if any *_enc column contains data, which
// signals a legacy deployment that used a raw key rather than the vault.
func hasEncryptedData(db *sql.DB) (bool, error) {
	checks := []string{
		`SELECT 1 FROM server_settings WHERE smtp_pass_enc != '' AND smtp_pass_enc IS NOT NULL LIMIT 1`,
		`SELECT 1 FROM server_settings WHERE google_client_secret_enc != '' AND google_client_secret_enc IS NOT NULL LIMIT 1`,
		`SELECT 1 FROM calendar_connections WHERE access_token_enc != '' AND access_token_enc IS NOT NULL LIMIT 1`,
		`SELECT 1 FROM webhooks WHERE secret_enc != '' AND secret_enc IS NOT NULL LIMIT 1`,
	}
	for _, q := range checks {
		var dummy int
		err := db.QueryRow(q).Scan(&dummy)
		if err == nil {
			return true, nil // found non-empty enc column
		}
		// ErrNoRows = table exists but no enc data; any other error = table may
		// not exist yet; either way, continue checking.
	}
	return false, nil
}

func ephemeralVault() (*Vault, error) {
	var dek [32]byte
	if _, err := rand.Read(dek[:]); err != nil {
		return nil, fmt.Errorf("keyvault: generate ephemeral DEK: %w", err)
	}
	return &Vault{dek: dek}, nil
}
