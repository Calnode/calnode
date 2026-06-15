-- +goose Up
CREATE TABLE server_settings (
    id              INTEGER PRIMARY KEY CHECK(id = 1),
    smtp_host       TEXT    NOT NULL DEFAULT '',
    smtp_port       TEXT    NOT NULL DEFAULT '587',
    smtp_user       TEXT    NOT NULL DEFAULT '',
    smtp_pass_enc   TEXT    NOT NULL DEFAULT '',
    smtp_tls        INTEGER NOT NULL DEFAULT 0,
    smtp_starttls   INTEGER NOT NULL DEFAULT 1,
    email_from      TEXT    NOT NULL DEFAULT '',
    email_from_name TEXT    NOT NULL DEFAULT 'Calnode',
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
-- Seed the single row so UPDATE statements always find it.
INSERT OR IGNORE INTO server_settings (id) VALUES (1);

-- +goose Down
DROP TABLE IF EXISTS server_settings;
