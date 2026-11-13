-- +goose Up
ALTER TABLE server_settings ADD COLUMN microsoft_client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN microsoft_client_secret_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN microsoft_tenant_id TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN microsoft_allowed_signup_domains TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE server_settings DROP COLUMN microsoft_allowed_signup_domains;
ALTER TABLE server_settings DROP COLUMN microsoft_tenant_id;
ALTER TABLE server_settings DROP COLUMN microsoft_client_secret_enc;
ALTER TABLE server_settings DROP COLUMN microsoft_client_id;
