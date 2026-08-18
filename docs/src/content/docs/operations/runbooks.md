---
title: "Operational runbooks"
description: "Symptom → diagnosis → action for the common production incidents, plus recommended control-plane alert rules."
---

Short runbooks for the incidents you're most likely to hit. Each is
**symptom → check → action**. Pair them with the [recommended alerts](#recommended-control-plane-alerts)
so you're paged with the runbook already in hand.

For **backup & restore** (PostgreSQL dump, ClickHouse backup to S3, and the
recovery procedure), see `RECOVERY.md` in the repository and the Helm
`backup.postgres` / `backup.clickhouse` CronJobs.

## Control plane returning 5xx / high latency

**Check** — the [health dashboard](/otel-fleet/installation/helm/#control-plane-health-dashboard):
5xx ratio, latency p95/p99, Go goroutines/heap, process CPU. `GET /healthz` and
`/readyz` on the ops port (`:9090`).

**Act** — if `/readyz` fails, a dependency is down: confirm PostgreSQL
reachability first (the control plane needs it; ClickHouse/VM outages degrade
reads but don't fail readiness). If CPU/latency-bound, add API replicas
(`controlPlane.api.replicas`, `mode: split`). If heap climbs unbounded, capture
a profile and roll the pods; check for an unusually large audit query or export.

## OpAMP tier down (edge agents disconnected)

**Symptom** — `otel_fleet_opamp_connected_agents` drops; agents show
disconnected in Fleet.

**Check** — the OpAMP tier pods (`mode: split` → the opamp Deployment) and the
`:4320` listener; agents dial out, so also check the LB / ingress for `:4320`
and (if enabled) client-cert mTLS.

**Act** — edge agents keep running on their **last-good config** while
disconnected (they cache it), so ingest continues; this is a control-plane
availability issue, not data loss. Restore the opamp pods; run ≥2 replicas for
HA. The singleton workers (retention sweep, alert evaluator) fail over
automatically via the Postgres advisory-lock leader election.

## ClickHouse full or unavailable

**Symptom** — Explore/overview/cost queries fail or return `503`; gateways'
export queue grows.

**Check** — ClickHouse disk usage and merge backlog; the gateway queue depth.

**Act** — reads degrade but the control plane stays up. To reclaim space, lower
the global TTL or set per-customer [retention overrides](/otel-fleet/installation/configuration/#data-lifecycle--erasure);
mutations are async. For a hard outage, gateways buffer to their persistent
queue and drain on recovery — size that volume for your worst tolerable outage.
Never point two clusters at the same disk. Shard when one node's disk or merge
throughput is the ceiling.

## VictoriaMetrics full / OOM

**Symptom** — throughput charts, the scoped metrics explorer, or PromQL alert
rules return errors; VM restarts.

**Check** — VM memory and active-series (cardinality). A cardinality spike
(often a new high-cardinality label from a pipeline) is the usual cause.

**Act** — give VM more memory or reduce retention; find and drop the offending
high-cardinality series at the pipeline. Enable [recording rules](/otel-fleet/installation/helm/#recording-rules)
so dashboards/alerts hit cheap pre-aggregated series instead of raw ones.

## Denial / auth spike (401 / 403 / 429)

**Symptom** — `otel_fleet_http_denied_total` rises.

**Check** — the `reason` label: `rate_limited` (a client hammering — expected
backpressure), `unauthenticated` / `invalid_api_token` (misconfigured client or
probing), `tenant_scope` / `requires_admin` (a scoped user hitting forbidden
areas — often a UI/permissions misconfig).

**Act** — for `rate_limited` floods from one IP, block upstream at the ingress;
remember the in-app limiter is [per-replica](/otel-fleet/installation/configuration/#rate-limiting-and-request-limits).
A sustained `unauthenticated` spike from many IPs is likely credential probing —
tighten the fronting WAF/ingress.

## DB migration failure on startup

**Symptom** — the control plane exits at boot with a migration error.

**Check** — the pod logs (goose runs migrations at startup); the PostgreSQL
version and connectivity.

**Act** — migrations are forward-only and run in order. Do **not** run two
different control-plane versions against one database concurrently during a
rollout of a schema-changing release; roll the whole control plane. Restore from
the pre-upgrade PostgreSQL backup if a migration is aborted mid-way, then retry
on a healthy database.

## Recommended control-plane alerts

Create these as **PromQL alert rules** in otel-fleet (Settings → Alert rules,
metric `promql`) — or via `POST /api/v1/settings/alert-rules` — against the
control plane's own metrics scraped into VictoriaMetrics. They fire to your
configured [notification channels](/otel-fleet/guides/alerting/).

| Alert | PromQL (`> threshold`) | Notes |
| --- | --- | --- |
| High 5xx ratio | `sum(rate(otel_fleet_http_requests_total{status="5xx"}[5m])) / clamp_min(sum(rate(otel_fleet_http_requests_total[5m])),1)` | page at `> 0.05` for 5m |
| High p99 latency | `histogram_quantile(0.99, sum(rate(otel_fleet_http_request_duration_seconds_bucket[5m])) by (le))` | warn at `> 1` (second) |
| No OpAMP connections | `sum(otel_fleet_opamp_connected_agents)` | alert on `< 1` if you run edge agents (breaks the "below" comparison) |
| Denial surge | `sum(rate(otel_fleet_http_denied_total[5m]))` | warn on an unusual sustained rate for your fleet |
| Control plane down | `up{job="otel-fleet-control-plane"}` | via your Prometheus/VM scrape target health |

Tune thresholds to your traffic. The [health dashboard](/otel-fleet/installation/helm/#control-plane-health-dashboard)
visualizes the same metrics for triage.
