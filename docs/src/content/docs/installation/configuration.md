---
title: "Configuration reference"
description: "The control plane is configured entirely through environment variables, all prefixed OTEL_FLEET_. Source of truth: internal/config/config.go."
---

The control plane is configured entirely through environment variables, all
prefixed `OTEL_FLEET_`. Source of truth: `internal/config/config.go`.

## Storage

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_DATABASE_URL` | `postgres://otelfleet:otelfleet@localhost:5432/otelfleet` | PostgreSQL DSN for control-plane state. Migrations run automatically at startup. |
| `OTEL_FLEET_CLICKHOUSE_ADDR` | `localhost:9000` | ClickHouse native-protocol `host:port` (used by the stats API; the collectors write via their own `CLICKHOUSE_ENDPOINT`). |
| `OTEL_FLEET_CLICKHOUSE_DATABASE` | `otel` | ClickHouse database with the telemetry tables (DDL in `deploy/clickhouse/schema/`). |
| `OTEL_FLEET_CLICKHOUSE_USER` | `otel-fleet` | ClickHouse user. |
| `OTEL_FLEET_CLICKHOUSE_PASSWORD` | `otel-fleet` | ClickHouse password. |
| `OTEL_FLEET_VICTORIAMETRICS_URL` | `http://localhost:8428` | Prometheus-compatible query endpoint for collector self-telemetry (powers stage metrics and throughput charts). |

### Data-residency regions (multi-region, Phase 1)

A customer is pinned to a **region** for data residency. Configure the region
registry with `OTEL_FLEET_REGIONS` (a JSON array); each region names a data
plane with its own telemetry stores. Unset (the default), a single `default`
region is synthesized from the `CLICKHOUSE_*` / `VICTORIAMETRICS_URL` settings
above, so single-region deployments need no change.

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_REGIONS` | *(synthesized `default`)* | JSON array of regions, e.g. `[{"name":"eu","displayName":"Europe","clickhouseAddr":"eu-ch:9000","clickhouseDatabase":"otel","victoriaMetricsUrl":"http://eu-vm:8428"},{"name":"us","clickhouseAddr":"us-ch:9000","victoriaMetricsUrl":"http://us-vm:8428"}]`. Names must be unique and non-empty. |
| `OTEL_FLEET_DEFAULT_REGION` | *(first region)* | Region assigned to new customers when none is specified; must be one of the configured regions. |

New customers take the default region unless one is chosen at creation; the
region is validated against the registry and returned on the customer. The
configured regions are also served at `GET /api/v1/regions` (drives the UI
selector).

Each region's `clickhouseAddr` / `clickhouseDatabase` / `victoriaMetricsUrl`
back its own telemetry stores (regions share the global `CLICKHOUSE_USER` /
`CLICKHOUSE_PASSWORD`). The control plane opens one ClickHouse connection per
region at startup.

:::note[What routes per region today]
**Customer-scoped reads route to the customer's region**: Explore (logs/traces),
per-customer throughput, and the scoped metrics explorer query that customer's
region's ClickHouse / VictoriaMetrics. **Still default-region only (Phase 2b):**
fleet-wide aggregates that span customers (the admin overview and cost
leaderboard), the admin ad-hoc PromQL panel, and the alerting evaluator +
retention sweep — these need cross-region fan-out. Single-region deployments
(one `default` region) are fully correct: everything resolves to the one region.
:::

## Listeners

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_HTTP_ADDR` | `:8080` | REST API + embedded web UI. |
| `OTEL_FLEET_GRPC_ADDR` | `:9443` | Internal gRPC (`otelfleet.auth.v1.AuthService`) used by gateway collectors to validate API keys. Plaintext — keep it cluster-internal. |
| `OTEL_FLEET_OPS_ADDR` | `:9090` | Ops listener: `/metrics`, `/healthz`, `/readyz`, and `GET /internal/v1/collector-config/forwarding` (the rendered forwarding-tier config). Plaintext — keep it cluster-internal. |
| `OTEL_FLEET_ROLE` | `all` | Process role: `all` (everything, single process), `api` (HTTP + gRPC + ops, scale to N), or `opamp` (OpAMP WebSockets + edge-config listener + webhooks + retention). The `opamp` role is HA-capable — run multiple replicas; the retention sweep and alert evaluator self-elect a single leader via a Postgres advisory lock. See Helm `controlPlane.mode`. |
| `OTEL_FLEET_OPAMP_ADDR` | `:4320` | OpAMP WebSocket server (`/v1/opamp`) for edge agents. Plaintext `ws://` — terminate TLS in front for internet exposure. |
| `OTEL_FLEET_TLS_CERT_FILE` / `_KEY_FILE` | _(empty)_ | PEM cert+key for the public listeners — HTTPS on :8080 and wss:// OpAMP on :4320. Empty = plaintext (terminate TLS at an ingress). |
| `OTEL_FLEET_GRPC_TLS_CERT_FILE` / `_KEY_FILE` | _(empty)_ | PEM cert+key for the internal gRPC AuthService (:9443). |
| `OTEL_FLEET_GRPC_CLIENT_CA_FILE` | _(empty)_ | When set, the gRPC AuthService requires a client cert signed by this CA (mTLS) — gateway collectors then present one via the `tenantauth` extension's `tls` block. |
| `OTEL_FLEET_OPAMP_PUBLIC_ENDPOINT` | _(empty)_ | Externally reachable OpAMP URL (e.g. `wss://opamp.example.com/v1/opamp`) offered to edge agents alongside their per-agent token. Empty = offer only the new auth header and let agents keep their current endpoint. |

## Web UI and sessions

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_BASE_URL` | `http://localhost:8080` | External URL of the control plane. Trailing slash is stripped. SSO redirect URIs are derived from it: `{BASE_URL}/auth/{name}/callback`. |
| `OTEL_FLEET_WEB_DIR` | *(empty)* | Directory with the built SPA to serve. Empty = API only (dev: run `pnpm dev` instead). The container image sets `/srv/otel-fleet/web`. |
| `OTEL_FLEET_SESSION_SECURE` | `false` | Set the `Secure` flag on session cookies. Enable whenever the UI is served over HTTPS. |

## Rate limiting and request limits

Per-client-IP token-bucket limiting and a request-body cap on the public HTTP
surface (`:8080`). OTLP ingest does not traverse this listener, so the body cap
only bounds control-API/UI requests. Disable rate limiting when a fronting
proxy already does it. The IP is taken from `X-Forwarded-For`/`X-Real-IP` (via
chi's RealIP), so run behind a trusted proxy that sets them.

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_RATE_LIMIT_ENABLED` | `true` | Master switch for per-IP rate limiting. |
| `OTEL_FLEET_RATE_LIMIT_RPS` | `50` | Sustained requests/sec per IP across the whole surface. |
| `OTEL_FLEET_RATE_LIMIT_BURST` | `100` | Burst bucket size per IP. |
| `OTEL_FLEET_AUTH_RATE_LIMIT_RPS` | `5` | Stricter sustained rate per IP layered on the SSO endpoints (`/auth/*`). |
| `OTEL_FLEET_AUTH_RATE_LIMIT_BURST` | `10` | Burst for the auth-endpoint bucket. |
| `OTEL_FLEET_MAX_REQUEST_BODY_BYTES` | `4194304` | Max request body (4 MiB) for `/api/v1`; larger bodies are rejected. `0` disables the cap. |

Exceeded limits return `429` with a `Retry-After` header. In Helm set these
under `controlPlane.rateLimit` (only keys you set are passed through).

### Denial audit

Every denied request (401 unauthenticated/unknown/disabled, 403 admin-only /
CSRF / insufficient-role / tenant-scope, 429 rate-limited) is recorded two ways
— never in the database, so unauthenticated floods can't grow persistent state:

- A structured `WARN` log line `security: request denied` with `reason`,
  `status`, `method`, `path`, `remote_ip`, and `actor` (the email when a
  session was authenticated, else `-`). Ship these to your log backend and
  alert on spikes.
- A Prometheus counter `otel_fleet_http_denied_total{reason}` on the ops
  `/metrics` endpoint (`reason` is a small fixed set: `unauthenticated`,
  `unknown_user`, `account_disabled`, `invalid_api_token`, `requires_admin`,
  `insufficient_role`, `csrf`, `rate_limited`, `tenant_scope`).

## Authentication and authorization

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_DEV_LOGIN` | `false` | Password-less login with any email (`POST /api/v1/auth/dev-login`). **Never enable outside local development/demos.** |
| `OTEL_FLEET_ADMIN_EMAILS` | *(empty)* | Comma-separated, case-insensitive list of emails that receive role `admin` on login. Everyone else starts as `viewer` (or with their invited role). |
| `OTEL_FLEET_MASTER_KEY` | *(empty)* | Base64-encoded 32-byte key for envelope encryption of secrets at rest. See [Secrets encryption](#secrets-encryption). |
| `OTEL_FLEET_MASTER_KEY_SECONDARY` | *(empty)* | Comma-separated old master keys, used **only to decrypt** during a [key rotation](#key-rotation). Deploy the new key as `OTEL_FLEET_MASTER_KEY`, the previous one here, re-encrypt, then drop it. |

### Environment-defined OIDC provider (bootstrap fallback)

SSO providers are normally managed in the UI (Settings → SSO, stored encrypted in
PostgreSQL — see the [SSO guide](/otel-fleet/guides/sso/)). A single generic OIDC
provider can additionally be configured via the environment; it appears on the
login page under the URL name `oidc` and is shadowed by a database provider with
the same name:

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_OIDC_ISSUER` | *(empty)* | Issuer URL; setting it enables the env provider. |
| `OTEL_FLEET_OIDC_CLIENT_ID` | *(empty)* | Required when the issuer is set. |
| `OTEL_FLEET_OIDC_CLIENT_SECRET` | *(empty)* | Client secret. |
| `OTEL_FLEET_OIDC_NAME` | `SSO` | Display name on the login page. |

## Secrets encryption

`OTEL_FLEET_MASTER_KEY` holds the base64-encoded 32-byte AES-256-GCM key used to
encrypt secrets at rest: SSO-provider client secrets and every pipeline
component field marked as a password in the catalog (e.g. exporter credentials).

```sh
openssl rand -base64 32
```

- **Not set:** the server boots and everything else works, but saving an SSO
  provider or a pipeline containing password fields fails with a clear error.
- **Lost without a secondary:** existing ciphertexts become unrecoverable
  (`cannot decrypt: data corrupted or wrong master key`). Treat the key as
  precious and back it up in your secret manager.

### Key rotation

The control plane supports **zero-downtime rotation**. It always encrypts with
the primary key (`OTEL_FLEET_MASTER_KEY`) but decrypts by trying the primary and
then any secondary keys — so old ciphertexts keep opening while new writes use
the new key.

1. **Generate** a new key: `openssl rand -base64 32`.
2. **Deploy** with the new key as `OTEL_FLEET_MASTER_KEY` and the current key
   moved to `OTEL_FLEET_MASTER_KEY_SECONDARY`. Everything decrypts; new/edited
   secrets are written under the new key.
3. **Re-encrypt** the stored discrete secrets under the new key: **Settings →
   SSO → Encryption at rest → “Re-encrypt under current key”** (admin), or
   `POST /api/v1/settings/reencrypt-secrets`. This rewrites SSO client secrets
   and webhook signing secrets; it is idempotent (already-migrated secrets are
   skipped). Pipeline exporter credentials re-key the next time their pipeline
   version is saved.
4. **Drop** `OTEL_FLEET_MASTER_KEY_SECONDARY` and redeploy once no ciphertext
   depends on the old key.

Multiple secondaries are allowed (comma-separated) if you are catching up on
several past keys at once.

## Pipeline validation and rollout

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_FLEET_OTELCOL_BIN` | `collector/dist/otel-fleet-collector` | Path to the collector distro binary used for authoritative `otelcol validate` of pipeline configs. Missing binary = validation degrades to structural checks only. The container image sets `/usr/local/bin/otel-fleet-collector`. |
| `OTEL_FLEET_DISTRIBUTOR` | `publish` | Forwarding-config rollout: `publish` (serve on the ops endpoint; collectors pick it up on restart) or `k8s` (patch an `OpenTelemetryCollector` CR; requires the opentelemetry-operator). Anything else fails startup. |
| `OTEL_FLEET_K8S_CR_NAME` | `otel-fleet-forwarding` | CR name patched in `k8s` mode. |
| `OTEL_FLEET_K8S_CR_NAMESPACE` | `otel-fleet` | CR namespace in `k8s` mode. |

## Related (not control-plane) variables

These configure the *collectors*, not the control plane, and appear in the compose
files and chart:

| Variable | Component | Description |
| --- | --- | --- |
| `OTEL_FLEET_AUTH_ENDPOINT` | gateway collector | Control-plane gRPC endpoint for the `tenantauth` extension (e.g. `control-plane:9443`). |
| `CLICKHOUSE_ENDPOINT` | gateway collector | ClickHouse exporter DSN, e.g. `tcp://clickhouse:9000?database=otel&username=...&password=...`. |
| `OTEL_FLEET_VM_REMOTE_WRITE` | gateway collector | Prometheus remote-write URL for ingest counters (default `http://victoriametrics:8428/api/v1/write`). |
| `OTEL_FLEET_FORWARD_ENDPOINT` | gateway collector | Forwarding-tier OTLP endpoint (default `forwarding:4317`). |
| `OTEL_FLEET_BOOTSTRAP_TOKEN` | edge agent (supervisor) | Per-customer enrollment token, injected into the OpAMP `Authorization` header. See [Edge agents](/otel-fleet/guides/edge-agents/). |
