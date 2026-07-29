-- otel-fleet ClickHouse metrics schema. We own the DDL (clickhouse exporter
-- runs with create_schema: false). Column layout matches the insert
-- statements of clickhouseexporter v0.156.0
-- (internal/sqltemplates/metrics_*_insert.sql); TenantId is MATERIALIZED from
-- the resource attribute stamped by the gateway and leads the ORDER BY so
-- per-customer queries are primary-key range scans.

CREATE DATABASE IF NOT EXISTS otel;

CREATE TABLE IF NOT EXISTS otel.otel_metrics_gauge
(
    ResourceAttributes    Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl     String CODEC (ZSTD(1)),
    ScopeName             String CODEC (ZSTD(1)),
    ScopeVersion          String CODEC (ZSTD(1)),
    ScopeAttributes       Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl        String CODEC (ZSTD(1)),
    ServiceName           LowCardinality(String) CODEC (ZSTD(1)),
    MetricName            String CODEC (ZSTD(1)),
    MetricDescription     String CODEC (ZSTD(1)),
    MetricUnit            String CODEC (ZSTD(1)),
    Attributes            Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix         DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix              DateTime64(9) CODEC (Delta, ZSTD(1)),
    Value                 Float64 CODEC (ZSTD(1)),
    Flags                 UInt32 CODEC (ZSTD(1)),
    Exemplars             Nested(
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime64(9),
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC (ZSTD(1)),

    TenantId              LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id']
)
ENGINE = MergeTree
PARTITION BY toDate(TimeUnix)
ORDER BY (TenantId, ServiceName, MetricName, toUnixTimestamp64Nano(TimeUnix))
TTL toDateTime(TimeUnix) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel.otel_metrics_sum
(
    ResourceAttributes     Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl      String CODEC (ZSTD(1)),
    ScopeName              String CODEC (ZSTD(1)),
    ScopeVersion           String CODEC (ZSTD(1)),
    ScopeAttributes        Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount  UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl         String CODEC (ZSTD(1)),
    ServiceName            LowCardinality(String) CODEC (ZSTD(1)),
    MetricName             String CODEC (ZSTD(1)),
    MetricDescription      String CODEC (ZSTD(1)),
    MetricUnit             String CODEC (ZSTD(1)),
    Attributes             Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix          DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix               DateTime64(9) CODEC (Delta, ZSTD(1)),
    Value                  Float64 CODEC (ZSTD(1)),
    Flags                  UInt32 CODEC (ZSTD(1)),
    Exemplars              Nested(
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime64(9),
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC (ZSTD(1)),
    AggregationTemporality Int32 CODEC (ZSTD(1)),
    IsMonotonic            Boolean CODEC (Delta, ZSTD(1)),

    TenantId               LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id']
)
ENGINE = MergeTree
PARTITION BY toDate(TimeUnix)
ORDER BY (TenantId, ServiceName, MetricName, toUnixTimestamp64Nano(TimeUnix))
TTL toDateTime(TimeUnix) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel.otel_metrics_histogram
(
    ResourceAttributes     Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl      String CODEC (ZSTD(1)),
    ScopeName              String CODEC (ZSTD(1)),
    ScopeVersion           String CODEC (ZSTD(1)),
    ScopeAttributes        Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount  UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl         String CODEC (ZSTD(1)),
    ServiceName            LowCardinality(String) CODEC (ZSTD(1)),
    MetricName             String CODEC (ZSTD(1)),
    MetricDescription      String CODEC (ZSTD(1)),
    MetricUnit             String CODEC (ZSTD(1)),
    Attributes             Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix          DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix               DateTime64(9) CODEC (Delta, ZSTD(1)),
    Count                  UInt64 CODEC (Delta, ZSTD(1)),
    Sum                    Float64 CODEC (ZSTD(1)),
    BucketCounts           Array(UInt64) CODEC (ZSTD(1)),
    ExplicitBounds         Array(Float64) CODEC (ZSTD(1)),
    Exemplars              Nested(
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime64(9),
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC (ZSTD(1)),
    Flags                  UInt32 CODEC (ZSTD(1)),
    Min                    Float64 CODEC (ZSTD(1)),
    Max                    Float64 CODEC (ZSTD(1)),
    AggregationTemporality Int32 CODEC (ZSTD(1)),

    TenantId               LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id']
)
ENGINE = MergeTree
PARTITION BY toDate(TimeUnix)
ORDER BY (TenantId, ServiceName, MetricName, toUnixTimestamp64Nano(TimeUnix))
TTL toDateTime(TimeUnix) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel.otel_metrics_exponential_histogram
(
    ResourceAttributes     Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl      String CODEC (ZSTD(1)),
    ScopeName              String CODEC (ZSTD(1)),
    ScopeVersion           String CODEC (ZSTD(1)),
    ScopeAttributes        Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount  UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl         String CODEC (ZSTD(1)),
    ServiceName            LowCardinality(String) CODEC (ZSTD(1)),
    MetricName             String CODEC (ZSTD(1)),
    MetricDescription      String CODEC (ZSTD(1)),
    MetricUnit             String CODEC (ZSTD(1)),
    Attributes             Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix          DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix               DateTime64(9) CODEC (Delta, ZSTD(1)),
    Count                  UInt64 CODEC (Delta, ZSTD(1)),
    Sum                    Float64 CODEC (ZSTD(1)),
    Scale                  Int32 CODEC (ZSTD(1)),
    ZeroCount              UInt64 CODEC (ZSTD(1)),
    PositiveOffset         Int32 CODEC (ZSTD(1)),
    PositiveBucketCounts   Array(UInt64) CODEC (ZSTD(1)),
    NegativeOffset         Int32 CODEC (ZSTD(1)),
    NegativeBucketCounts   Array(UInt64) CODEC (ZSTD(1)),
    Exemplars              Nested(
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime64(9),
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC (ZSTD(1)),
    Flags                  UInt32 CODEC (ZSTD(1)),
    Min                    Float64 CODEC (ZSTD(1)),
    Max                    Float64 CODEC (ZSTD(1)),
    AggregationTemporality Int32 CODEC (ZSTD(1)),

    TenantId               LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id']
)
ENGINE = MergeTree
PARTITION BY toDate(TimeUnix)
ORDER BY (TenantId, ServiceName, MetricName, toUnixTimestamp64Nano(TimeUnix))
TTL toDateTime(TimeUnix) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel.otel_metrics_summary
(
    ResourceAttributes    Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ResourceSchemaUrl     String CODEC (ZSTD(1)),
    ScopeName             String CODEC (ZSTD(1)),
    ScopeVersion          String CODEC (ZSTD(1)),
    ScopeAttributes       Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    ScopeDroppedAttrCount UInt32 CODEC (ZSTD(1)),
    ScopeSchemaUrl        String CODEC (ZSTD(1)),
    ServiceName           LowCardinality(String) CODEC (ZSTD(1)),
    MetricName            String CODEC (ZSTD(1)),
    MetricDescription     String CODEC (ZSTD(1)),
    MetricUnit            String CODEC (ZSTD(1)),
    Attributes            Map(LowCardinality(String), String) CODEC (ZSTD(1)),
    StartTimeUnix         DateTime64(9) CODEC (Delta, ZSTD(1)),
    TimeUnix              DateTime64(9) CODEC (Delta, ZSTD(1)),
    Count                 UInt64 CODEC (Delta, ZSTD(1)),
    Sum                   Float64 CODEC (ZSTD(1)),
    ValueAtQuantiles      Nested(
        Quantile Float64,
        Value Float64
    ) CODEC (ZSTD(1)),
    Flags                 UInt32 CODEC (ZSTD(1)),

    TenantId              LowCardinality(String) MATERIALIZED ResourceAttributes['tenant.id']
)
ENGINE = MergeTree
PARTITION BY toDate(TimeUnix)
ORDER BY (TenantId, ServiceName, MetricName, toUnixTimestamp64Nano(TimeUnix))
TTL toDateTime(TimeUnix) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- Per-minute ingest counts for metric data points, mirroring the logs/traces
-- materialized views in 001_otel_tables.sql.
CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_metrics_gauge_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'metrics' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(TimeUnix)) AS Minute,
    count() AS Items
FROM otel.otel_metrics_gauge
GROUP BY TenantId, ServiceName, Minute;

CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_metrics_sum_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'metrics' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(TimeUnix)) AS Minute,
    count() AS Items
FROM otel.otel_metrics_sum
GROUP BY TenantId, ServiceName, Minute;

CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_metrics_histogram_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'metrics' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(TimeUnix)) AS Minute,
    count() AS Items
FROM otel.otel_metrics_histogram
GROUP BY TenantId, ServiceName, Minute;

CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_metrics_exponential_histogram_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'metrics' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(TimeUnix)) AS Minute,
    count() AS Items
FROM otel.otel_metrics_exponential_histogram
GROUP BY TenantId, ServiceName, Minute;

CREATE MATERIALIZED VIEW IF NOT EXISTS otel.mv_ingest_metrics_summary_1m TO otel.ingest_counts_1m AS
SELECT
    TenantId,
    'metrics' AS Signal,
    ServiceName,
    toStartOfMinute(toDateTime(TimeUnix)) AS Minute,
    count() AS Items
FROM otel.otel_metrics_summary
GROUP BY TenantId, ServiceName, Minute;
