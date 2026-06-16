-- +goose Up
CREATE TABLE crypto_keystore (
    id          INTEGER PRIMARY KEY,
    label       TEXT    NOT NULL UNIQUE,   -- 'primary' | 'recovery'
    wrapped_dek BLOB    NOT NULL,          -- DEK encrypted under this entry's KEK
    kdf         TEXT    NOT NULL,          -- 'argon2id'
    kdf_salt    BLOB    NOT NULL,          -- 16 random bytes
    kdf_params  TEXT    NOT NULL,          -- JSON: {"m":65536,"t":3,"p":2}
    dek_version INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS crypto_keystore;
