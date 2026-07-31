-- +goose Up

-- Maintenance windows silence ALL alert firing while active (e.g. during a
-- planned deploy). The evaluator skips its entire pass when now is inside any
-- window, so no alert.firing/alert.resolved is sent; evaluation resumes cleanly
-- when the window ends.
CREATE TABLE alert_maintenance_windows (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    starts_at  TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT alert_maintenance_windows_range_check CHECK (ends_at > starts_at)
);

-- Fast "is now inside a window" lookup.
CREATE INDEX alert_maintenance_windows_active_idx ON alert_maintenance_windows (starts_at, ends_at);

-- +goose Down
DROP TABLE alert_maintenance_windows;
