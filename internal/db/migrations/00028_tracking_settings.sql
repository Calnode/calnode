-- +goose Up
-- Tracking / analytics settings (instance-wide, on the singleton row):
--   head_html          raw HTML/JS injected into the <head> of the public booking
--                      and manage pages (GTM/GA4/Pixel snippets, etc.).
--   tracking_csp_allow  optional space-separated CSP source allowlist; when set, the
--                      relaxed public-page CSP is tightened to just these origins.
--   datalayer_enabled   push booking/cancel/reschedule events into window.dataLayer.
--   datalayer_fields    JSON array of field keys to include in those pushes.
ALTER TABLE server_settings ADD COLUMN head_html TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN tracking_csp_allow TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN datalayer_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_settings ADD COLUMN datalayer_fields TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE server_settings DROP COLUMN head_html;
ALTER TABLE server_settings DROP COLUMN tracking_csp_allow;
ALTER TABLE server_settings DROP COLUMN datalayer_enabled;
ALTER TABLE server_settings DROP COLUMN datalayer_fields;
