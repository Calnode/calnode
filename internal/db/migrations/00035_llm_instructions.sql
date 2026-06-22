-- +goose Up
-- Admin "Additional instructions" appended to the assistant's base system prompt
-- (tone, business context, do's/don'ts). The base prompt — the tool-calling contract +
-- safety rails — stays in code; this is the customization layer only.
ALTER TABLE server_settings ADD COLUMN llm_extra_instructions TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE server_settings DROP COLUMN llm_extra_instructions;
