# tenantauth extension

Server authenticator (`extensionauth.Server`) for the otel-fleet gateway.
Receivers reference it via `auth: {authenticator: tenantauth}`; every request
must present an ingest API key in `Authorization: Bearer <key>` or `X-Api-Key`
(header names and bearer scheme are case-insensitive).

Keys are validated against the control plane's `otelfleet.auth.v1.AuthService`
(`proto/authservice.proto`; this module generates its own stubs, see
`buf.gen.yaml`). On success the resolved identity is attached to `client.Info`
as `client.AuthData` with these attributes:

| Attribute                  | Type   | Consumed by | Meaning |
|----------------------------|--------|-------------|---------|
| `tenant.id`                | string | tenantstamp | Tenant identity stamped on resources |
| `client.id`                | string | tenantstamp | Same as tenant.id today |
| `customer.id`              | string | tenantstamp | Owning customer UUID |
| `rate_limit_items_per_sec` | int64  | tenantquota | Per-tenant ingest quota in items/sec; `0` = unlimited |

`rate_limit_items_per_sec` is an **int64** (not a string). It is cached with
the same TTL as the identity, so a limit change in the control plane takes
effect within `cache.ttl` (30s in the shipped gateway config).

## Caching / failure behavior

- Cache key is the SHA-256 of the presented key (raw keys are never stored).
- Positive results are cached for `min(cache.ttl, cache_ttl_seconds from the
  server)`; negative results for `cache.negative_ttl`.
- If the control plane is unreachable, expired positive entries are served
  stale for up to `cache.stale_if_error` (fail-open for known keys); unknown
  keys are rejected (fail-closed).
- LRU eviction above `cache.max_entries`.

## Telemetry

`otel_fleet_auth_requests_total` counter, attributes `outcome`
(`ok` | `invalid_key` | `no_key` | `upstream_error_stale` |
`upstream_error_reject`) and `tenant_id` (when known).

## Configuration

```yaml
extensions:
  tenantauth:
    endpoint: ${env:OTEL_FLEET_AUTH_ENDPOINT} # control-plane gRPC address
    insecure: true                           # dev only: plaintext gRPC
    cache:
      ttl: 30s
      negative_ttl: 5s
      max_entries: 50000
      stale_if_error: 15m
```
