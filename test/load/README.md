# Load & capacity harness

Two small, repeatable load tests to sanity-check throughput/latency and produce
rough numbers for the [sizing guide](../../docs/src/content/docs/operations/sizing.md).

> These are **capacity-planning aids, not benchmarks of record**. Numbers from a
> dev laptop under Docker Desktop are *indicative only* — the control plane, the
> gateway, and ClickHouse all contend for the same few cores. Run against
> production-like hardware (dedicated nodes, real disks) for numbers you size on.

## Prerequisites

- The compose data plane up:
  ```sh
  docker compose -f deploy/compose/docker-compose.yaml up -d postgres clickhouse victoriametrics gateway
  ```
- The control plane running on the host with dev-login enabled:
  ```sh
  OTEL_FLEET_DEV_LOGIN=true OTEL_FLEET_ADMIN_EMAILS=js@sag-solutions.com make run
  ```
- `curl` + `jq` (ingest script), Docker (pulls `telemetrygen`), Go (API tool).

## Ingest throughput

Mints a customer + API key, blasts OTLP through the gateway with
[`telemetrygen`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/cmd/telemetrygen)
for a window, then reads the row count back from ClickHouse to get the effective
end-to-end rate (ingest → auth → tenantstamp → quota → batch → export → ClickHouse):

```sh
test/load/ingest.sh logs 30 8      # signal, seconds, workers
test/load/ingest.sh traces 30 8
```

Have a local `telemetrygen`? Skip the Docker pull with `TELEMETRYGEN=telemetrygen`.

## Control-plane API

Drives one endpoint with N workers for a fixed duration and reports throughput +
latency percentiles (p50/p90/p95/p99):

```sh
# fleet overview (admin aggregate)
go run ./test/load/apiload -c 50 -d 20s \
  -path '/api/v1/stats/overview?from=2020-01-01T00:00:00Z&to=2030-01-01T00:00:00Z'

# explore logs for a customer (tenant-scoped read)
go run ./test/load/apiload -c 50 -d 20s \
  -path '/api/v1/customers/<id>/logs?from=...&to=...&limit=100'
```

`apiload` dev-logs-in, so the control plane needs `OTEL_FLEET_DEV_LOGIN=true`.

## Interpreting results

- **Ingest** is throughput-bound and scales horizontally — add gateway replicas /
  enable KEDA. The single-gateway number here is a per-replica floor.
- **API read latency** grows with ClickHouse load and result size; scale the API
  tier (`controlPlane.api.replicas`) for concurrency and give ClickHouse
  headroom for query load.
- Watch the [control-plane dashboard](../../docs/src/content/docs/installation/helm.mdx)
  and ClickHouse metrics while a test runs to see which component saturates first.

See the [sizing guide](../../docs/src/content/docs/operations/sizing.md) for how
these map to production capacity.
