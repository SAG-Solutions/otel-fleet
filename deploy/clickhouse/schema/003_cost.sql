-- P5: byte accounting for the cost dashboard. ingest_counts_1m gains a Bytes
-- column (estimated in-memory row size via byteSize(*), not compressed-at-rest
-- size); the materialized views are recreated to feed it. MVs only apply to
-- new inserts — historic rows keep Bytes=0, which the cost API documents.
--
-- Existing dev volumes need this applied manually (initdb only runs on fresh
-- volumes): docker exec otel-fleet-dev-clickhouse-1 clickhouse-client
--   --user otelfleet --password otelfleet --queries-file /docker-entrypoint-initdb.d/003_cost.sql

ALTER TABLE otel.ingest_counts_1m ADD COLUMN IF NOT EXISTS Bytes UInt64 AFTER Items;

DROP TABLE IF EXISTS otel.mv_ingest_logs_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_logs_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'logs' AS Signal,
    ServiceName,
    toStartOfMinute(TimestampTime) AS Minute,
    count() AS Items,
    sum(byteSize(*)) AS Bytes
FROM otel.otel_logs
GROUP BY TenantId, ServiceName, Minute;

DROP TABLE IF EXISTS otel.mv_ingest_traces_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_traces_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'traces' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(Timestamp)) AS Minute,
    count() AS Items,
    sum(byteSize(*)) AS Bytes
FROM otel.otel_traces
GROUP BY TenantId, ServiceName, Minute;

-- NOTE: the SummingMergeTree over (TenantId, Signal, ServiceName, Minute)
-- sums Bytes like Items automatically. The metrics-table MVs
-- (002_otel_metrics.sql) are updated in place below for the same reason.

DROP TABLE IF EXISTS otel.mv_ingest_metrics_gauge_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_metrics_gauge_1m TO otel.ingest_counts_1m AS
SELECT TenantId, 'metrics' AS Signal, ServiceName,
       toStartOfMinute(toDateTime(TimeUnix)) AS Minute, count() AS Items, sum(byteSize(*)) AS Bytes
FROM otel.otel_metrics_gauge GROUP BY TenantId, ServiceName, Minute;

DROP TABLE IF EXISTS otel.mv_ingest_metrics_sum_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_metrics_sum_1m TO otel.ingest_counts_1m AS
SELECT TenantId, 'metrics' AS Signal, ServiceName,
       toStartOfMinute(toDateTime(TimeUnix)) AS Minute, count() AS Items, sum(byteSize(*)) AS Bytes
FROM otel.otel_metrics_sum GROUP BY TenantId, ServiceName, Minute;

DROP TABLE IF EXISTS otel.mv_ingest_metrics_histogram_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_metrics_histogram_1m TO otel.ingest_counts_1m AS
SELECT TenantId, 'metrics' AS Signal, ServiceName,
       toStartOfMinute(toDateTime(TimeUnix)) AS Minute, count() AS Items, sum(byteSize(*)) AS Bytes
FROM otel.otel_metrics_histogram GROUP BY TenantId, ServiceName, Minute;

DROP TABLE IF EXISTS otel.mv_ingest_metrics_exponential_histogram_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_metrics_exponential_histogram_1m TO otel.ingest_counts_1m AS
SELECT TenantId, 'metrics' AS Signal, ServiceName,
       toStartOfMinute(toDateTime(TimeUnix)) AS Minute, count() AS Items, sum(byteSize(*)) AS Bytes
FROM otel.otel_metrics_exponential_histogram GROUP BY TenantId, ServiceName, Minute;

DROP TABLE IF EXISTS otel.mv_ingest_metrics_summary_1m;
CREATE MATERIALIZED VIEW otel.mv_ingest_metrics_summary_1m TO otel.ingest_counts_1m AS
SELECT TenantId, 'metrics' AS Signal, ServiceName,
       toStartOfMinute(toDateTime(TimeUnix)) AS Minute, count() AS Items, sum(byteSize(*)) AS Bytes
FROM otel.otel_metrics_summary GROUP BY TenantId, ServiceName, Minute;
