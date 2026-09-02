---
title: "Sizing & capacity"
description: "How to size the control plane, data plane, and stores for a given tenant count and ingest rate, and which levers to scale."
---

otel-fleet separates a small stateful **control plane** from a horizontally
scalable **data plane** (gateways) and three stores (PostgreSQL, ClickHouse,
VictoriaMetrics). You scale each independently. These are starting points —
measure with the [control-plane health dashboard](/installation/helm/#control-plane-health-dashboard)
and your ClickHouse/VM metrics, then adjust.

## Components at a glance

| Component | Scales with | Lever |
| --- | --- | --- |
| Control plane — API tier | admin/UI + API traffic, agent count | `controlPlane.mode: split` → `controlPlane.api.replicas` |
| Control plane — OpAMP tier | number of **edge agents** (persistent WebSockets) | `controlPlane.opamp.replicas` (HA via leader election) |
| Gateway collectors | **ingest throughput** (events/sec, bytes/sec) | replicas / KEDA autoscaling |
| PostgreSQL | fleet **metadata** size (customers, users, pipelines, audit) | vertical; managed PG fine |
| ClickHouse | **telemetry volume** + retention window | vertical + disk; shard for very large fleets |
| VictoriaMetrics | metric **series cardinality** + retention | vertical + disk |

The control plane never stores telemetry — only metadata — so its footprint is
driven by request rate and agent count, not ingest volume.

## Starting points

**Control plane.** Start at `mode: all`, 1 replica, `250m` CPU / `256Mi` for
small fleets. Move to `mode: split` when you want independent scaling or zero
single points of failure:

- **API tier**: 2 replicas behind the LB; add replicas when the dashboard shows
  sustained CPU pressure or p95 latency climbing under load. Stateless — scale freely.
- **OpAMP tier**: budget for persistent connections, not throughput. Rough guide
  **~1 vCPU / 2 GiB per ~2–5k connected edge agents** per replica; run ≥2 for HA
  (the singleton workers — retention sweep, alert evaluator — self-elect one leader).

**Gateways.** Throughput-bound. As a rough starting point a single gateway
replica (`1` vCPU / `1–2 GiB`) handles on the order of **tens of thousands of
spans/logs per second**; the real number depends on payload size, processors,
and exporters. Enable KEDA autoscaling (`keda.enabled`) to scale on the queue /
CPU rather than guessing. Give the gateway a persistent queue volume so bursts
and brief backend outages don't drop data.

**ClickHouse.** The dominant cost. Plan disk from your ingest rate × retention:
`daily_bytes ≈ events/day × avg_bytes/event × compression (~0.1–0.2)`, then
× retention days, plus headroom for merges (~30–50%). Per-customer
[retention overrides](/installation/configuration/#data-lifecycle--erasure)
and the global TTL bound growth. Give it fast local disk and memory for merges;
shard once a single node's disk or merge throughput is the limit.

**VictoriaMetrics.** Sized by active series (cardinality), not raw event count.
The `tenant_id` label plus per-service/per-signal labels drive cardinality;
watch `vm_rows` / active-series metrics and give VM headroom (2–4 GiB is plenty
for modest fleets). Cluster VM for very high cardinality.

**PostgreSQL.** Small. Even large fleets keep metadata in the low GBs; the audit
log is the main grower — a managed Postgres with routine backups is enough.

## Scaling levers, in order

1. **Gateways** for ingest — replicas / KEDA. This is what grows with customer traffic.
2. **ClickHouse disk + retention** — the storage bill; tune TTL and per-customer overrides.
3. **API replicas** — for UI/API responsiveness under many operators or high agent churn.
4. **OpAMP replicas** — for edge-agent connection count and HA.
5. **VictoriaMetrics** — vertical first; cluster on cardinality pressure.

## Measure, don't guess

The control-plane dashboard (request rate, 5xx ratio, latency p50/p95/p99,
denials, OpAMP connected agents, Go/process runtime) tells you when the control
plane needs more headroom. For the data plane, watch ClickHouse disk/merge
metrics and VM active-series. Set the [alerts](/operations/runbooks/#recommended-control-plane-alerts)
so you're paged before saturation, not after.

A repeatable load harness lives in [`test/load/`](https://github.com/SAG-Solutions/otel-fleet/tree/main/test/load):
an ingest test (`ingest.sh`, via `telemetrygen`) and an API test (`apiload`,
throughput + latency percentiles). Run it against production-like hardware for
numbers you can size on.

### Indicative baseline

Very rough, single control-plane process, rate limiting off, **on a dev laptop
with every component (control plane, ClickHouse, PostgreSQL) contending for the
same cores** — a lower bound, not a capacity claim. Dedicated nodes will do
better; the point is the *shape*:

| Endpoint | Throughput | p50 / p99 | Bound by |
| --- | --- | --- | --- |
| `/api/v1/me` (auth + session) | ~12,000 req/s | 4 ms / 7 ms | PostgreSQL session lookup |
| `/api/v1/stats/overview` (fleet aggregate, 10 tenants) | ~1,200 req/s | 28 ms / 220 ms | ClickHouse `GROUP BY` |

The cheap auth path is ~10× the aggregate-read path: control-plane capacity is
dominated by **query cost**, not the framework. Scale the API tier for read
concurrency and give ClickHouse headroom; the auth/session path is rarely the
bottleneck. Gather ingest throughput with `test/load/ingest.sh` on real hardware
— on a laptop the gateway and ClickHouse contend, so that number isn't
meaningful.
