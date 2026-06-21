-- +goose Up
-- Records whether a connected Microsoft calendar is a work/school account or a
-- personal Microsoft account. Personal accounts can't mint Teams-for-Business
-- links, so this gates whether a "teams" event type can auto-generate one.
-- '' = unknown (legacy rows / Google) → treated as capable; real value is
-- captured from the id_token tenant claim on (re)connect.
ALTER TABLE calendar_connections ADD COLUMN account_kind TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE calendar_connections DROP COLUMN account_kind;
