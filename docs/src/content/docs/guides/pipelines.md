---
title: "Pipelines"
description: "Pipelines describe what happens to a customer's data after ingest: processors \u2192 exporters, built in the UI (or via POST /api/v1/customers/{id}/pipelines),\u2026"
---

Pipelines describe what happens to a customer's data after ingest: processors →
exporters, built in the UI (or via `POST /api/v1/customers/{id}/pipelines`),
versioned, validated, and rolled out to either the central forwarding tier or the
customer's edge agents.

## Target classes

| | `forwarding` (default) | `edge` |
| --- | --- | --- |
| Runs on | the central forwarding tier | the customer's edge agents (via OpAMP) |
| Input | copy of the customer's ingested data, routed by `tenant.id` | whatever the edge agent receives locally (OTLP :4317/:4318) |
| Rollout | activate → serve rendered config → collector restart (or CR patch) | activate → pushed live over OpAMP |

The two classes are rendered by isolated renderers; a customer can have both.

## Component catalog

The builder offers a curated catalog (`GET /api/v1/catalog/components`), each
backed by a JSON Schema that drives the form and validation:

- **Processors:** Batch, Memory Limiter, Filter, Transform, Attributes, Resource
- **Exporters:** OTLP (gRPC), OTLP (HTTP), Debug, ClickHouse,
  Prometheus Remote Write, File, Elasticsearch, Datadog, Kafka, AWS S3,
  Splunk HEC

Receivers are not part of the graph — the renderer provides them (routing
connector input on the forwarding tier; an OTLP receiver on edge agents).

### Backend presets

"Add exporter" opens a gallery of **backend presets** — one click drops in the
right exporter with its endpoint/auth fields pre-filled for a specific backend,
each with its logo:

| Preset | Underlying exporter |
|---|---|
| Grafana Loki | OTLP (HTTP) → Loki's OTLP endpoint |
| Grafana Tempo, Jaeger | OTLP (gRPC) |
| Grafana Mimir / Prometheus | Prometheus Remote Write |
| Grafana Cloud (OTLP) | OTLP (HTTP) + Basic auth header |
| Elasticsearch, Datadog, Kafka, AWS S3, Splunk HEC | their dedicated exporters |

A preset is pure convenience: the rendered pipeline is just the underlying
exporter with the pre-filled config, so you can tweak every field afterwards.
The "All exporters" tab in the same dialog adds any raw exporter. Exporters can
only target backends whose exporter is compiled into the collector distro
(`collector/builder-config.yaml`).

## Versioning and rollout

Every save creates an **immutable version**: the graph, the rendered collector
config fragment, and its hash. Activation moves a version pointer — rollback is
just activating an older version.

```mermaid
flowchart LR
    edit["Edit graph<br/>(schema-driven forms)"] --> validate
    validate["Validate<br/>structural + otelcol validate"] --> save["Save version N"]
    save --> activate["Activate"]
    activate --> render["Control plane re-renders the<br/>full forwarding config"]
    render --> rollout["Rollout"]
    rollout -->|compose / Helm deployment mode| restart["collector restart<br/>(status: pending_restart)"]
    rollout -->|Helm operator mode| patch["OpenTelemetryCollector CR patch"]
    rollout -->|edge| push["OpAMP push to connected agents"]
```

### Validation is the real thing

Validation happens in two stages:

1. **Structural** — graph and JSON-Schema checks with error paths that map back
   to form fields (e.g. `exporters[0].config.endpoint`).
2. **Authoritative** — the control plane renders the full collector config and
   runs `otelcol validate` with the **actual distro binary**
   (`OTEL_FLEET_OTELCOL_BIN`). What validates here is exactly what the collectors
   will load. If the binary is missing, only structural validation runs.

### Forwarding-tier rollout mechanics

The forwarding collector loads its *entire* config from the control plane's ops
endpoint (`GET :9090/internal/v1/collector-config/forwarding`) via the HTTP
confmap provider.

- **compose / Helm `deployment` mode** (`OTEL_FLEET_DISTRIBUTOR=publish`):
  activation re-renders and re-serves the config; running collectors keep the old
  one until restarted. The pipeline shows `pending_restart` —
  `docker compose restart forwarding` or
  `kubectl rollout restart deployment/otel-fleet-forwarding` applies it.
- **Helm `operator` mode** (`OTEL_FLEET_DISTRIBUTOR=k8s`): the control plane
  patches the `OpenTelemetryCollector` CR and the opentelemetry-operator rolls
  the pods; no manual restart.

### Edge rollout mechanics

Edge pipelines are rendered as a standalone per-customer collector config (all of
the customer's active edge pipelines merged, sharing one OTLP receiver) and pushed
over OpAMP to every connected agent of that customer immediately on activation.
See [Edge agents](/guides/edge-agents/).

## Secrets in pipeline configs

Component fields marked as passwords in the catalog (credentials on exporters)
are:

- **encrypted at rest** with `OTEL_FLEET_MASTER_KEY` (AES-256-GCM) — saving a
  pipeline with password fields fails cleanly if the key is not configured;
- **never returned** by the API — reads show a redaction sentinel, and saving a
  graph with the sentinel copies the previously stored secret forward, so the
  plaintext never round-trips through the browser.

## Stage metrics

The pipeline detail page shows per-stage throughput:

- **received** per signal, from ClickHouse ingest data;
- **sent / failed / queued** per exporter, from collector self-telemetry in
  VictoriaMetrics. Rendered component IDs encode the pipeline
  (`<type>/<customerSlug>__<pipelineSlug>__<node>`), which is what makes
  per-pipeline attribution of `otelcol_exporter_*` metrics possible — see
  [the metric contract](/architecture/#the-metric-contract).
