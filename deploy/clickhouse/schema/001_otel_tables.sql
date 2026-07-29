-- otel-fleet ClickHouse schema. We own the DDL (clickhouse exporter runs with
-- create_schema: false): TenantId leads the ORDER BY so every per-customer
-- time-range query is a primary-key range scan. TenantId is MATERIALIZED from
-- the resource attribute stamped by the gateway, so the exporter's default
-- INSERT column list keeps working unchanged.
--
-- Column layout must match the pinned clickhouseexporter version
-- (collector/builder-config.yaml); verified by the compose e2e test.

CREATE DATABASE IF NOT EXISTS otel;

CREATE TABLE IF NOT EXISTS otel.otel_logs
(
    Timestamp          DateTime64(9) CODEC (Delta(8), ZSTD(1)),
    TimestampTime      DateTime DEFAULT toDateTime(Timestamp),
    TraceId            String CODEC (ZSTD(1)),
    SpanId             String CODEC (ZSTD(1)),
    TraceFlags         UInt8,
    SeverityText       LowCardinality(String) CODEC (ZSTD(1)),
    SeverityNumber     UInt8,
    ServiceName        LowCardinality(String) CODEC (ZSTD(1)),
    Body               String CODEC (ZSTD(1)),
    ResourceSchemaUrl  LowCardinality(String) CODEC (ZSTD(1)),
    ResourceAttributes Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeSchemaUrl     LowCardinality(String) CODEC (ZSTD(1)),
    ScopeName          String CODEC (ZSTD(1)),
    ScopeVersion       LowCardinality(String) CODEC (ZSTD(1)),
    ScopeAttributes    Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    LogAttributes      Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    -- The clickhouseexporter (>= v0.156.0) detects this column via DESCRIBE
    -- and includes it in inserts when present.
    EventName          String CODEC (ZSTD(1)),

    TenantId           LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id'],

    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_body Body TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 8
)
ENGINE = MergeTree
PARTITION BY toDate(TimestampTime)
ORDER BY (TenantId, ServiceName, TimestampTime)
TTL TimestampTime + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel.otel_traces
(
    Timestamp          DateTime64(9) CODEC (Delta(8), ZSTD(1)),
    TraceId            String CODEC (ZSTD(1)),
    SpanId             String CODEC (ZSTD(1)),
    ParentSpanId       String CODEC (ZSTD(1)),
    TraceState         String CODEC (ZSTD(1)),
    SpanName           LowCardinality(String) CODEC (ZSTD(1)),
    SpanKind           LowCardinality(String) CODEC (ZSTD(1)),
    ServiceName        LowCardinality(String) CODEC (ZSTD(1)),
    ResourceAttributes Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeName          String CODEC (ZSTD(1)),
    ScopeVersion       String CODEC (ZSTD(1)),
    SpanAttributes     Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    Duration           UInt64 CODEC (ZSTD(1)),
    StatusCode         LowCardinality(String) CODEC (ZSTD(1)),
    StatusMessage      String CODEC (ZSTD(1)),
    Events             Nested(
        Timestamp DateTime64(9),
        Name LowCardinality(String),
        Attributes Map(LowCardinality(String), String)
    ) CODEC (ZSTD(1)),
    Links              Nested(
        TraceId String,
        SpanId String,
        TraceState String,
        Attributes Map(LowCardinality(String), String)
    ) CODEC (ZSTD(1)),

    TenantId           LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id'],

    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (TenantId, ServiceName, toDateTime(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- Per-minute ingest counts per tenant and signal: the UI's "ground truth
-- stored" series. Fed by materialized views below; queried by the stats API.
CREATE TABLE IF NOT EXISTS otel.ingest_counts_1m
(
    TenantId    LowCardinality(String),
    Signal      LowCardinality(String), -- 'logs' | 'traces' | 'metrics'
    ServiceName LowCardinality(String),
    Minute      DateTime,
    Items       UInt64
)
ENGINE = SummingMergeTree(Items)
PARTITION BY toDate(Minute)
ORDER BY (TenantId, Signal, ServiceName, Minute)
TTL Minute + INTERVAL 90 DAY
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_logs_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'logs' AS Signal,
    ServiceName,
    toStartOfMinute(TimestampTime) AS Minute,
    count() AS Items
FROM otel.otel_logs
GROUP BY TenantId, ServiceName, Minute;

CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_traces_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'traces' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(Timestamp)) AS Minute,
    count() AS Items
FROM otel.otel_traces
GROUP BY TenantId, ServiceName, Minute;
