---
title: "Alerting & notifications"
description: "otel-fleet can notify you when something needs attention \u2014 an agent goes offline, a config rollout fails, or a customer's ingest volume crosses a\u2026"
---

otel-fleet can notify you when something needs attention — an agent goes
offline, a config rollout fails, or a customer's ingest volume crosses a
threshold you defined. Notifications are delivered to **channels** you
configure once and then reference from alert sources.

There are two independent sources of alerts:

- **Fleet events** — an agent goes offline, reports an unhealthy status, or
  fails to apply a pushed config. These fire automatically; you only choose
  which channels receive them.
- **Metric-threshold rules** — you define a condition over a per-customer
  metric (e.g. "ingest stopped" or "too many error logs") that is evaluated on
  a schedule and fires when crossed.

Both are configured under **Settings** (admin only).

## Notification channels

A channel is a delivery target. Four types are supported:

| Type | Delivery |
|---|---|
| **Webhook** | An HTTPS `POST` of a JSON body. Optionally HMAC-signed with a shared secret (`X-Otelfleet-Signature: sha256=…`) so the receiver can verify authenticity. |
| **Slack** | A Slack [incoming-webhook](https://api.slack.com/messaging/webhooks) URL; the body is formatted as a Slack message with a coloured attachment. |
| **PagerDuty** | A [PagerDuty Events API v2](https://developer.pagerduty.com/docs/events-api-v2-overview) event. A firing alert **triggers** an incident and its matching resolve **resolves** it (correlated by a stable `dedup_key`). Alert severity maps to PagerDuty severity; the secret is the integration **routing key**. |
| **Opsgenie** | An [Opsgenie Alert API](https://docs.opsgenie.com/docs/alert-api) alert. Firing **creates** an alert (keyed by `alias`); resolve **closes** it. Alert severity maps to priority `P1`–`P5`; the secret is a **GenieKey** API key (sent as `Authorization: GenieKey …`). |

Create channels under **Settings → Notification channels**:

1. **New channel** → pick the type.
2. **URL** — for Webhook it must be `https://` (plain `http://` only for
   `localhost`); for Slack paste the incoming-webhook URL. For PagerDuty and
   Opsgenie the URL is **optional** — leave it blank to use the default vendor
   endpoint, or set a region-specific one (e.g. `https://api.eu.opsgenie.com/v2/alerts`).
3. **Secret** — an optional HMAC signing key for generic webhooks; the
   **required** routing key / API key for PagerDuty / Opsgenie; unused for Slack.
   Stored encrypted at rest and never returned in API responses.
4. **Events** — which fleet events this channel subscribes to. (Metric-threshold
   rules reference channels directly, so this list does not affect them.)
5. **Test** sends a sample payload so you can confirm the endpoint works before
   relying on it.

PagerDuty and Opsgenie auto-resolve: because the resolve carries the same
`dedup_key`/`alias` as the fire, the incident/alert it opened is closed
automatically when the condition clears — no manual cleanup.

### Webhook payload

Generic webhooks receive a JSON body:

```json
{
  "event": "alert.firing",
  "occurredAt": "2026-07-28T14:17:21Z",
  "detail": {
    "rule": "ingest stopped",
    "customer": "ACME Corp",
    "metric": "ingest_items",
    "comparison": "below",
    "threshold": 100,
    "observed": 0,
    "windowSeconds": 300
  }
}
```

Fleet events carry agent identity in `detail` instead (agent id, class,
customer). If a signing secret is set, the raw body is HMAC-SHA256 signed and
the digest is sent in the `X-Otelfleet-Signature` header. Delivery is retried
with backoff on transient failures.

## Metric-threshold alert rules

Rules live under **Settings → Alert rules**. Each rule watches one metric for
one customer (or all customers) over a rolling window and fires its channels
when the value crosses the threshold.

| Field | Meaning |
|---|---|
| **Metric** | `ingest_items` — total records (logs + spans + metric points) ingested. `error_logs` — count of log records with severity ≥ `ERROR`. `promql` — evaluate an arbitrary PromQL query against VictoriaMetrics (see below). |
| **Comparison** | `below` fires when the observed value is **less than** the threshold (e.g. ingest stopped). `above` fires when it is **greater than** the threshold (e.g. error spike). |
| **Threshold** | The numeric boundary. |
| **Window** | The rolling look-back (minimum 60 s). The metric is summed/counted over `now − window … now`. Ignored for `promql` (the query carries its own range). |
| **Scope** | A single customer, or **All customers** — in which case the rule is evaluated independently per customer and fires per customer. `promql` rules are always cluster-wide (no customer scope). |
| **Channels** | Which notification channels receive the firing/resolved notification. |
| **Enabled** | Toggle without deleting. |

### How evaluation works

The evaluator runs on the `opamp` tier, alongside retention and the webhook
dispatcher. With multiple OpAMP replicas it runs on exactly one at a time
(PostgreSQL advisory-lock leader election), so alerts never fire twice. Every
minute the leader:

1. Loads all enabled rules.
2. For each rule, computes the metric per in-scope customer over the window
   (against ClickHouse). A customer with no data reads as `0` — this is what
   makes a `below` rule detect *ingest stopped*.
3. Compares to the threshold and tracks firing state **in memory**:
   - On the transition healthy → breaching, it sends `alert.firing` once.
   - On the transition breaching → healthy, it sends `alert.resolved` once.
   - While the state is unchanged, nothing is sent (no repeat spam).

Because firing state is in memory, a control-plane restart may re-notify an
already-breaching rule once. Deleting or disabling a rule while it is firing
does **not** send a resolved notification.

### PromQL rules (infrastructure alerting)

A `promql` rule runs an instant PromQL **query** against VictoriaMetrics each
tick and compares the returned scalar to the threshold — the same engine and
channels, over cluster/infra metrics instead of per-tenant ClickHouse
aggregates. This makes otel-fleet's own alerting a lightweight replacement for
vmalert/Alertmanager on top of the [cluster monitoring](../installation/helm/)
bundle, with no extra service to run.

- The query should aggregate to a **single value** (e.g. `avg(...)`,
  `sum(...)`, `max(...)`); if it returns a vector, the first sample is used.
- An **empty result is treated as "no data"**, not a breach — a transiently
  missing metric won't flap the alert.
- `customerId` must be null and `query` must be non-empty (enforced on create).

Example: `sum(rate(otel_fleet_http_denied_total[5m]))` **above** `10` → alerts
when denied requests spike; `avg(k8s_node_cpu_utilization)` **above** `0.85` →
node CPU saturation. Requires VictoriaMetrics reachable from the control plane
(`OTEL_FLEET_VICTORIAMETRICS_URL`).

### Maintenance windows

To avoid alert noise during planned work, create a **maintenance window**
(Settings → Alert rules → Maintenance windows) with a start and end time. While
`now` is inside any active window, the evaluator skips its entire pass — **no
rule fires or resolves**, across all rules. Firing state is left untouched, so
evaluation resumes cleanly when the window ends (a rule that was already firing
before the window stays firing without re-notifying; one that started breaching
during the window fires on the first tick after it ends).

### Severity inhibition

During an incident many lower-severity alerts often fire alongside the real
cause. Set **`OTEL_FLEET_ALERT_INHIBIT_LOWER_SEVERITY=true`** (Helm
`controlPlane.alertInhibitLowerSeverity`) and the evaluator **suppresses firing
notifications for `warning`/`info` alerts in a scope while a `critical` alert is
firing in that same scope** — the customer for metric-threshold rules, or the
region for cluster PromQL rules. The firing **state is still tracked**, so the
suppressed alert's **resolve still notifies**, and the critical itself always
notifies. Off by default (each rule notifies independently). This is scoped
inhibition, not full routing trees — for label-based routing/grouping keep an
Alertmanager via the cluster-monitoring [`notifierUrl`](/otel-fleet/operations/migrating-from-kube-prometheus/#keeping-alertmanager-optional-bridge).

### Examples

- **Ingest stopped for a key customer** — Metric `ingest_items`, comparison
  `below`, threshold `1`, window `5m`, scope *ACME Corp*. Fires if ACME sends
  nothing for five minutes.
- **Error-log spike, any customer** — Metric `error_logs`, comparison `above`,
  threshold `50`, window `5m`, scope *All customers*. Fires per customer whose
  `ERROR`+ logs exceed 50 in five minutes.

## API

Channels:

- `GET`/`POST` `/api/v1/settings/webhooks`, `PATCH`/`DELETE`
  `/api/v1/settings/webhooks/{id}`, `POST /api/v1/settings/webhooks/{id}/test`.

Alert rules:

- `GET`/`POST` `/api/v1/settings/alert-rules`
- `PATCH`/`DELETE` `/api/v1/settings/alert-rules/{ruleId}`

All are admin-only and usable with a management-API token (`otm_pat_…`) for
config-as-code.
