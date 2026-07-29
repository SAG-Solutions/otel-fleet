---
title: "Pipeline templates"
description: "Copy-paste starting points for common pipelines. Each graph block is exactly what the pipeline builder produces and what otel-fleetctl apply consumes \u2014\u2026"
---

Copy-paste starting points for common pipelines. Each `graph` block is exactly
what the pipeline builder produces and what `otel-fleetctl apply` consumes — drop
one under a pipeline in your [config-as-code](/otel-fleet/guides/cli/) spec, or rebuild it in the
UI. Every graph is validated against the real collector binary before it can be
activated, so a bad template fails fast with an inline error.

The receiver side is implicit (the tenant's routed stream); a graph is
`signals` + an ordered `processors` chain + one or more `exporters`.

## Forward to a customer's OTLP backend

The bread-and-butter forwarding pipeline: batch and ship to the customer's own
collector / Grafana / vendor OTLP endpoint.

```yaml
targetClass: forwarding
graph:
  signals: [logs, traces, metrics]
  processors:
    - type: memory_limiter
      config: { check_interval: 1s, limit_percentage: 80, spike_limit_percentage: 20 }
    - type: batch
      config: { send_batch_size: 8192, timeout: 5s }
  exporters:
    - type: otlphttp
      config:
        endpoint: https://otlp.customer.example.com
        headers: { authorization: "Bearer <redacted>" }   # set once; kept on re-apply
```

## Store in ClickHouse only

Keep the data in otel-fleet's ClickHouse (queryable in **Explore**), no
forwarding.

```yaml
targetClass: forwarding
graph:
  signals: [logs, traces]
  processors:
    - type: batch
      config: { send_batch_size: 8192, timeout: 5s }
  exporters:
    - type: clickhouse
      config: { create_schema: false }
```

## Drop noisy logs (cost control)

Filter out low-severity or health-check noise before it is stored or forwarded.

```yaml
targetClass: forwarding
graph:
  signals: [logs]
  processors:
    - type: filter
      config:
        error_mode: ignore
        logs:
          log_record:
            - 'severity_number < SEVERITY_NUMBER_WARN'
            - 'IsMatch(attributes["url.path"], "/healthz|/readyz")'
    - type: batch
      config: {}
  exporters:
    - type: otlphttp
      config: { endpoint: https://otlp.customer.example.com }
```

## Redact PII

Scrub sensitive attributes with the `transform` / `attributes` processor before
the data leaves the tier.

```yaml
targetClass: forwarding
graph:
  signals: [logs, traces]
  processors:
    - type: attributes
      config:
        actions:
          - { key: user.email, action: delete }
          - { key: http.request.header.authorization, action: delete }
          - { key: enduser.id, action: hash }
    - type: batch
      config: {}
  exporters:
    - type: otlphttp
      config: { endpoint: https://otlp.customer.example.com }
```

## Add a tenant/site label

Stamp extra resource attributes (e.g. environment, region) onto everything.

```yaml
targetClass: forwarding
graph:
  signals: [logs, traces, metrics]
  processors:
    - type: resource
      config:
        attributes:
          - { key: deployment.environment, value: production, action: upsert }
    - type: batch
      config: {}
  exporters:
    - type: otlphttp
      config: { endpoint: https://otlp.customer.example.com }
```

## Edge agent → central gateway

An `edge` pipeline runs on a customer-site agent (managed via OpAMP). Keep the
edge light: batch and forward to your central gateway; do the heavy processing
centrally.

```yaml
targetClass: edge
graph:
  signals: [logs, traces, metrics]
  processors:
    - type: batch
      config: { send_batch_size: 4096, timeout: 5s }
  exporters:
    - type: otlp
      config: { endpoint: gateway.example.com:4317, tls: { insecure: false } }
```

## Available components

Processors: `memory_limiter`, `batch`, `filter`, `transform`, `attributes`,
`resource`. Exporters: `otlp`, `otlphttp`, `clickhouse`, `prometheusremotewrite`,
`file`, `debug`. Each exposes a schema-driven form in the builder; the
[REST API](/otel-fleet/reference/api/) `GET /catalog/components` returns the full
JSON-schema catalog.
