-- +goose Up
-- Optional LLM layer config (PRD §8.11), stored like SMTP/Google settings on the
-- single server_settings row. Provider-agnostic: any OpenAI-compatible chat-completions
-- endpoint. Off by default; api key encrypted at rest (CALNODE_ENCRYPTION_KEY).
ALTER TABLE server_settings ADD COLUMN llm_endpoint    TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN llm_model       TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN llm_api_key_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN llm_enabled     INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN llm_enabled;
ALTER TABLE server_settings DROP COLUMN llm_api_key_enc;
ALTER TABLE server_settings DROP COLUMN llm_model;
ALTER TABLE server_settings DROP COLUMN llm_endpoint;
