# tenantquota processor

Enforces the per-tenant ingest quota resolved by the `tenantauth` extension.
For every batch it reads `tenant.id` and `rate_limit_items_per_sec` (an
**int64**; `0`/absent = unlimited) from the `client.Info` auth data and runs a
per-tenant token bucket:

- capacity = `rate_limit_items_per_sec x burst_seconds` tokens (bucket starts
  full, so a tenant may burst `burst_seconds` worth of items at once),
- refill = `rate_limit_items_per_sec` tokens/sec,
- items are counted as **log records, spans and metric data points** (not
  metrics).

The budget is shared across the logs, traces and metrics pipelines: all
per-signal instances of one configured `tenantquota` component use a single
bucket registry. Buckets are created lazily per tenant and garbage-collected
after ~10 minutes without traffic (a GC'd tenant simply gets a fresh, full
bucket on its next request). Limit changes propagate via the tenantauth cache
(within its `cache.ttl`, 30s in the shipped gateway config) and resize the
bucket in place.

## Rejection semantics

A batch that does not fit is rejected **whole** (no partial split) and
consumes **nothing** from the bucket — including single batches larger than
the burst capacity, which are rejected immediately (never blocked). The
processor returns a gRPC `RESOURCE_EXHAUSTED` status error carrying a
`RetryInfo` detail (retry delay derived from the token deficit, clamped to
100ms–30s). It is deliberately **not** `consumererror.NewPermanent`: the
otlpreceiver (v0.156) forwards a ready-made gRPC status verbatim, so clients
see

- OTLP/gRPC: `RESOURCE_EXHAUSTED` + `RetryInfo` (retryable/throttling signal
  for OTLP exporters, which only retry `RESOURCE_EXHAUSTED` when `RetryInfo`
  is present),
- OTLP/HTTP: `429 Too Many Requests`.

## Placement

**After `tenantstamp`, before `batch`**:

```yaml
processors: [memory_limiter, tenantstamp, tenantquota, batch]
```

- It needs the per-request auth context (`client.Info`), which the `batch`
  processor discards — and `tenantstamp` upstream guarantees unauthenticated
  data has already been dropped.
- It must run before `batch` so the rejection propagates synchronously to the
  sending client as backpressure instead of being swallowed by an async
  export queue.

## Configuration

```yaml
processors:
  tenantquota:
    burst_seconds: 2 # bucket capacity = limit * burst_seconds (default 2)
```

## Telemetry

- `otel_fleet_quota_decisions_total{tenant_id, decision=allowed|rejected}` —
  admission decisions per batch (batches of unlimited tenants are not
  counted).
- `otel_fleet_quota_rejected_items_total{tenant_id, signal}` — items rejected
  by the quota.
