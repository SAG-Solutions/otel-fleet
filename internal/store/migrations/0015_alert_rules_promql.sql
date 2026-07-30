-- +goose Up

-- PromQL alert rules: evaluate an arbitrary PromQL query against VictoriaMetrics
-- and fire when the returned value crosses the threshold. These are
-- cluster/infra-wide (not per-customer), so metric='promql' rules carry a
-- non-empty query and a NULL customer_id.
ALTER TABLE alert_rules ADD COLUMN query TEXT NOT NULL DEFAULT '';

ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_check
    CHECK (metric IN ('ingest_items', 'error_logs', 'promql'));

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_promql_check
    CHECK (metric <> 'promql' OR (query <> '' AND customer_id IS NULL));

-- +goose Down
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_promql_check;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_check
    CHECK (metric IN ('ingest_items', 'error_logs'));
ALTER TABLE alert_rules DROP COLUMN query;
