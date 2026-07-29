# tenantstamp processor

Stamps the authenticated tenant identity onto every `Resource` of traces,
logs and metrics passing through the pipeline:

| Resource attribute | Source (`client.AuthData`) |
| ------------------ | -------------------------- |
| `tenant.id`        | `tenant.id`                |
| `client.id`        | `client.id`                |
| `customer.id`      | `customer.id`              |

Behavior:

- **Spoofing defense**: any pre-existing `tenant.id`, `client.id` or
  `customer.id` resource attributes sent by the client are deleted before the
  authenticated values are set. This happens even when the authenticated
  identity has no value for one of them.
- **Missing auth → drop**: if the incoming context carries no authentication
  data (or no `tenant.id`), the whole batch is dropped (never forwarded), the
  `otel_fleet_tenantstamp_dropped_batches_total` counter is incremented
  (attribute `signal`), and a throttled warning is logged.

## Requirements

This processor reads `client.Info` from the request context. It only works
when the receivers in front of it attach authentication data, i.e. the
receiver **must** be configured with the `tenantauth` server authenticator
(and, for metadata propagation, `include_metadata: true`):

```yaml
extensions:
  tenantauth:
    endpoint: ${env:OTEL_FLEET_AUTH_ENDPOINT}

receivers:
  otlp:
    protocols:
      grpc:
        auth:
          authenticator: tenantauth
        include_metadata: true
      http:
        auth:
          authenticator: tenantauth
        include_metadata: true

processors:
  tenantstamp: {}
```

Note: the `batch` processor (and any other processor that regroups data)
discards the per-request context, so `tenantstamp` must run **before**
`batch` in the pipeline.

## Configuration

The processor takes no configuration options:

```yaml
processors:
  tenantstamp: {}
```
