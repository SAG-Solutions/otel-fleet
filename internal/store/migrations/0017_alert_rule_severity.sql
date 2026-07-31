-- +goose Up

-- Per-rule severity, carried into the notification payload (and used to colour
-- Slack messages). Existing rules default to 'warning'.
ALTER TABLE alert_rules ADD COLUMN severity TEXT NOT NULL DEFAULT 'warning'
    CHECK (severity IN ('info', 'warning', 'critical'));

-- +goose Down
ALTER TABLE alert_rules DROP COLUMN severity;
