# Alerting & notifications

otelfleet can notify you when something needs attention — an agent goes
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

A channel is a delivery target. Two types are supported:

| Type | Delivery |
|---|---|
| **Webhook** | An HTTPS `POST` of a JSON body. Optionally HMAC-signed with a shared secret (`X-Otelfleet-Signature: sha256=…`) so the receiver can verify authenticity. |
| **Slack** | A Slack [incoming-webhook](https://api.slack.com/messaging/webhooks) URL; the body is formatted as a Slack message with a coloured attachment. |

Create channels under **Settings → Notification channels**:

1. **New channel** → pick the type.
2. **URL** — must be `https://` (plain `http://` is allowed only for
   `localhost`, for local testing). For Slack, paste the incoming-webhook URL.
3. **Secret** (generic webhooks only) — optional HMAC signing key. Stored
   encrypted at rest and never returned in API responses.
4. **Events** — which fleet events this channel subscribes to. (Metric-threshold
   rules reference channels directly, so this list does not affect them.)
5. **Test** sends a sample payload so you can confirm the endpoint works before
   relying on it.

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
| **Metric** | `ingest_items` — total records (logs + spans + metric points) ingested. `error_logs` — count of log records with severity ≥ `ERROR`. |
| **Comparison** | `below` fires when the observed value is **less than** the threshold (e.g. ingest stopped). `above` fires when it is **greater than** the threshold (e.g. error spike). |
| **Threshold** | The numeric boundary. |
| **Window** | The rolling look-back (minimum 60 s). The metric is summed/counted over `now − window … now`. |
| **Scope** | A single customer, or **All customers** — in which case the rule is evaluated independently per customer and fires per customer. |
| **Channels** | Which notification channels receive the firing/resolved notification. |
| **Enabled** | Toggle without deleting. |

### How evaluation works

The evaluator runs on the singleton (`opamp`) tier, alongside retention and the
webhook dispatcher. Every minute it:

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
