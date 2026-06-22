-- +goose Up
-- OAuth 2.1 authorization-server tables backing the MCP "Connect" flow (PRD §11
-- authorization). Calnode is its own AS for the /mcp resource: clients self-register
-- (dynamic client registration, public PKCE clients), the workspace owner authorizes
-- via the existing session/Google/Microsoft login + a consent screen, and bearer
-- access tokens (hashed, like api_keys) gate /mcp. All token/code values are stored as
-- SHA-256 hashes — the plaintext is only ever returned once to the client.

CREATE TABLE oauth_clients (
    client_id     TEXT PRIMARY KEY,
    client_name   TEXT NOT NULL DEFAULT '',
    redirect_uris TEXT NOT NULL,            -- JSON array of allowed redirect URIs
    created_at    TEXT NOT NULL
);

CREATE TABLE oauth_auth_codes (
    code_hash      TEXT PRIMARY KEY,        -- SHA-256 of the authorization code
    client_id      TEXT NOT NULL,
    user_id        TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,           -- PKCE S256 challenge
    scope          TEXT NOT NULL DEFAULT '',
    resource       TEXT NOT NULL DEFAULT '', -- RFC 8707 resource indicator
    expires_at     TEXT NOT NULL,           -- short-lived (single use)
    created_at     TEXT NOT NULL
);

CREATE TABLE oauth_access_tokens (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,      -- SHA-256 of the access token
    refresh_hash TEXT UNIQUE,               -- SHA-256 of the refresh token (nullable)
    client_id    TEXT NOT NULL,
    user_id      TEXT NOT NULL,
    scope        TEXT NOT NULL DEFAULT '',
    resource     TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL,             -- access-token expiry
    created_at   TEXT NOT NULL,
    last_used_at TEXT
);

CREATE INDEX idx_oauth_tokens_user ON oauth_access_tokens(user_id);
CREATE INDEX idx_oauth_tokens_refresh ON oauth_access_tokens(refresh_hash);

-- +goose Down
DROP INDEX idx_oauth_tokens_refresh;
DROP INDEX idx_oauth_tokens_user;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_auth_codes;
DROP TABLE oauth_clients;
