# Configuration reference

The control plane is configured entirely through environment variables, all
prefixed `OTELFLEET_`. Source of truth: `internal/config/config.go`.

## Storage

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_DATABASE_URL` | `postgres://otelfleet:otelfleet@localhost:5432/otelfleet` | PostgreSQL DSN for control-plane state. Migrations run automatically at startup. |
| `OTELFLEET_CLICKHOUSE_ADDR` | `localhost:9000` | ClickHouse native-protocol `host:port` (used by the stats API; the collectors write via their own `CLICKHOUSE_ENDPOINT`). |
| `OTELFLEET_CLICKHOUSE_DATABASE` | `otel` | ClickHouse database with the telemetry tables (DDL in `deploy/clickhouse/schema/`). |
| `OTELFLEET_CLICKHOUSE_USER` | `otelfleet` | ClickHouse user. |
| `OTELFLEET_CLICKHOUSE_PASSWORD` | `otelfleet` | ClickHouse password. |
| `OTELFLEET_VICTORIAMETRICS_URL` | `http://localhost:8428` | Prometheus-compatible query endpoint for collector self-telemetry (powers stage metrics and throughput charts). |

## Listeners

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_HTTP_ADDR` | `:8080` | REST API + embedded web UI. |
| `OTELFLEET_GRPC_ADDR` | `:9443` | Internal gRPC (`otelfleet.auth.v1.AuthService`) used by gateway collectors to validate API keys. Plaintext — keep it cluster-internal. |
| `OTELFLEET_OPS_ADDR` | `:9090` | Ops listener: `/metrics`, `/healthz`, `/readyz`, and `GET /internal/v1/collector-config/forwarding` (the rendered forwarding-tier config). Plaintext — keep it cluster-internal. |
| `OTELFLEET_ROLE` | `all` | Process role: `all` (everything, single process), `api` (HTTP + gRPC + ops, scale to N), or `opamp` (OpAMP WebSockets + edge-config listener + webhooks + retention, singleton). See Helm `controlPlane.mode`. |
| `OTELFLEET_OPAMP_ADDR` | `:4320` | OpAMP WebSocket server (`/v1/opamp`) for edge agents. Plaintext `ws://` — terminate TLS in front for internet exposure. |
| `OTELFLEET_TLS_CERT_FILE` / `_KEY_FILE` | _(empty)_ | PEM cert+key for the public listeners — HTTPS on :8080 and wss:// OpAMP on :4320. Empty = plaintext (terminate TLS at an ingress). |
| `OTELFLEET_GRPC_TLS_CERT_FILE` / `_KEY_FILE` | _(empty)_ | PEM cert+key for the internal gRPC AuthService (:9443). |
| `OTELFLEET_GRPC_CLIENT_CA_FILE` | _(empty)_ | When set, the gRPC AuthService requires a client cert signed by this CA (mTLS) — gateway collectors then present one via the `tenantauth` extension's `tls` block. |
| `OTELFLEET_OPAMP_PUBLIC_ENDPOINT` | _(empty)_ | Externally reachable OpAMP URL (e.g. `wss://opamp.example.com/v1/opamp`) offered to edge agents alongside their per-agent token. Empty = offer only the new auth header and let agents keep their current endpoint. |

## Web UI and sessions

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_BASE_URL` | `http://localhost:8080` | External URL of the control plane. Trailing slash is stripped. SSO redirect URIs are derived from it: `{BASE_URL}/auth/{name}/callback`. |
| `OTELFLEET_WEB_DIR` | *(empty)* | Directory with the built SPA to serve. Empty = API only (dev: run `pnpm dev` instead). The container image sets `/srv/otelfleet/web`. |
| `OTELFLEET_SESSION_SECURE` | `false` | Set the `Secure` flag on session cookies. Enable whenever the UI is served over HTTPS. |

## Rate limiting and request limits

Per-client-IP token-bucket limiting and a request-body cap on the public HTTP
surface (`:8080`). OTLP ingest does not traverse this listener, so the body cap
only bounds control-API/UI requests. Disable rate limiting when a fronting
proxy already does it. The IP is taken from `X-Forwarded-For`/`X-Real-IP` (via
chi's RealIP), so run behind a trusted proxy that sets them.

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_RATE_LIMIT_ENABLED` | `true` | Master switch for per-IP rate limiting. |
| `OTELFLEET_RATE_LIMIT_RPS` | `50` | Sustained requests/sec per IP across the whole surface. |
| `OTELFLEET_RATE_LIMIT_BURST` | `100` | Burst bucket size per IP. |
| `OTELFLEET_AUTH_RATE_LIMIT_RPS` | `5` | Stricter sustained rate per IP layered on the SSO endpoints (`/auth/*`). |
| `OTELFLEET_AUTH_RATE_LIMIT_BURST` | `10` | Burst for the auth-endpoint bucket. |
| `OTELFLEET_MAX_REQUEST_BODY_BYTES` | `4194304` | Max request body (4 MiB) for `/api/v1`; larger bodies are rejected. `0` disables the cap. |

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
- A Prometheus counter `otelfleet_http_denied_total{reason}` on the ops
  `/metrics` endpoint (`reason` is a small fixed set: `unauthenticated`,
  `unknown_user`, `account_disabled`, `invalid_api_token`, `requires_admin`,
  `insufficient_role`, `csrf`, `rate_limited`, `tenant_scope`).

## Authentication and authorization

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_DEV_LOGIN` | `false` | Password-less login with any email (`POST /api/v1/auth/dev-login`). **Never enable outside local development/demos.** |
| `OTELFLEET_ADMIN_EMAILS` | *(empty)* | Comma-separated, case-insensitive list of emails that receive role `admin` on login. Everyone else starts as `viewer` (or with their invited role). |
| `OTELFLEET_MASTER_KEY` | *(empty)* | Base64-encoded 32-byte key for envelope encryption of secrets at rest. See [Secrets encryption](#secrets-encryption). |

### Environment-defined OIDC provider (bootstrap fallback)

SSO providers are normally managed in the UI (Settings → SSO, stored encrypted in
PostgreSQL — see the [SSO guide](../guides/sso.md)). A single generic OIDC
provider can additionally be configured via the environment; it appears on the
login page under the URL name `oidc` and is shadowed by a database provider with
the same name:

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_OIDC_ISSUER` | *(empty)* | Issuer URL; setting it enables the env provider. |
| `OTELFLEET_OIDC_CLIENT_ID` | *(empty)* | Required when the issuer is set. |
| `OTELFLEET_OIDC_CLIENT_SECRET` | *(empty)* | Client secret. |
| `OTELFLEET_OIDC_NAME` | `SSO` | Display name on the login page. |

## Secrets encryption

`OTELFLEET_MASTER_KEY` holds the base64-encoded 32-byte AES-256-GCM key used to
encrypt secrets at rest: SSO-provider client secrets and every pipeline
component field marked as a password in the catalog (e.g. exporter credentials).

```sh
openssl rand -base64 32
```

- **Not set:** the server boots and everything else works, but saving an SSO
  provider or a pipeline containing password fields fails with a clear error.
- **Lost/rotated without re-encryption:** existing ciphertexts become
  unrecoverable (`cannot decrypt: data corrupted or wrong master key`). There is
  no automatic key-rotation yet (the ciphertext format is versioned to allow it
  later); treat the key as precious.

## Pipeline validation and rollout

| Variable | Default | Description |
| --- | --- | --- |
| `OTELFLEET_OTELCOL_BIN` | `collector/dist/otelfleet-collector` | Path to the collector distro binary used for authoritative `otelcol validate` of pipeline configs. Missing binary = validation degrades to structural checks only. The container image sets `/usr/local/bin/otelfleet-collector`. |
| `OTELFLEET_DISTRIBUTOR` | `publish` | Forwarding-config rollout: `publish` (serve on the ops endpoint; collectors pick it up on restart) or `k8s` (patch an `OpenTelemetryCollector` CR; requires the opentelemetry-operator). Anything else fails startup. |
| `OTELFLEET_K8S_CR_NAME` | `otelfleet-forwarding` | CR name patched in `k8s` mode. |
| `OTELFLEET_K8S_CR_NAMESPACE` | `otelfleet` | CR namespace in `k8s` mode. |

## Related (not control-plane) variables

These configure the *collectors*, not the control plane, and appear in the compose
files and chart:

| Variable | Component | Description |
| --- | --- | --- |
| `OTELFLEET_AUTH_ENDPOINT` | gateway collector | Control-plane gRPC endpoint for the `tenantauth` extension (e.g. `control-plane:9443`). |
| `CLICKHOUSE_ENDPOINT` | gateway collector | ClickHouse exporter DSN, e.g. `tcp://clickhouse:9000?database=otel&username=...&password=...`. |
| `OTELFLEET_VM_REMOTE_WRITE` | gateway collector | Prometheus remote-write URL for ingest counters (default `http://victoriametrics:8428/api/v1/write`). |
| `OTELFLEET_FORWARD_ENDPOINT` | gateway collector | Forwarding-tier OTLP endpoint (default `forwarding:4317`). |
| `OTELFLEET_BOOTSTRAP_TOKEN` | edge agent (supervisor) | Per-customer enrollment token, injected into the OpAMP `Authorization` header. See [Edge agents](../guides/edge-agents.md). |
