-- +goose Up

-- Metric-threshold alert rules. A periodic evaluator computes each rule's
-- metric per in-scope customer over its window and fires the referenced
-- notification channels when the threshold is crossed.
--   metric      : ingest_items (sum of ingested items) | error_logs (count of
--                 severity>=ERROR log records)
--   comparison  : below | above  (below detects an ingest drop/outage)
--   customer_id : NULL = evaluate for every active customer
--   channel_ids : webhooks.id[] the rule notifies
CREATE TABLE alert_rules (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    metric         TEXT NOT NULL CHECK (metric IN ('ingest_items', 'error_logs')),
    comparison     TEXT NOT NULL CHECK (comparison IN ('below', 'above')),
    threshold      DOUBLE PRECISION NOT NULL,
    window_seconds INT NOT NULL CHECK (window_seconds >= 60),
    customer_id    UUID REFERENCES customers(id) ON DELETE CASCADE,
    channel_ids    UUID[] NOT NULL DEFAULT '{}',
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX alert_rules_enabled_idx ON alert_rules (enabled) WHERE enabled;

-- +goose Down
DROP TABLE alert_rules;
