# Sending data

Customers send OpenTelemetry data (logs, traces, metrics) to the gateway over
**OTLP**, authenticated with the customer's **API key**. otel-fleet validates the
key, stamps the tenant identity onto every record, and stores and/or forwards
it per the customer's pipelines.

## 1. Get an API key

Create a customer in the UI (**Customers → New customer**) or with the CLI /
API; the initial API key is shown **once**. Create more under the customer's
**API keys** tab. A key looks like `otm_ab12cd34_…`.

Key revocation reaches the gateways within the ~30 s auth cache.

## 2. Point your OTLP exporter at the gateway

| Protocol | Endpoint (default ports) |
|---|---|
| OTLP/gRPC | `<gateway-host>:4317` |
| OTLP/HTTP | `http://<gateway-host>:4318` |

Send the key in the `Authorization` header:

```
Authorization: Bearer otm_ab12cd34_…
```

In production the gateway sits behind TLS — use `https://` / gRPC-TLS and your
real hostname. In the local dev stack the gateway is `localhost:4317` / `:4318`
(plaintext).

## 3. Configure your source

### An application via an OTel SDK

Set the standard OTLP environment variables — every language SDK reads them:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp.example.com:4317"
export OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer otm_ab12cd34_…"
export OTEL_SERVICE_NAME="checkout"
```

That is all most SDKs need. Language-specific setup (auto-instrumentation
agents, `OTEL_TRACES_EXPORTER=otlp`, etc.) follows the upstream
[OpenTelemetry docs](https://opentelemetry.io/docs/languages/); only the
endpoint + `authorization` header are otel-fleet-specific.

### A collector you already run (agent → otel-fleet gateway)

Add otel-fleet as an OTLP exporter in your own collector:

```yaml
exporters:
  otlp/otel-fleet:
    endpoint: otlp.example.com:4317
    headers:
      authorization: "Bearer otm_ab12cd34_…"

service:
  pipelines:
    traces: { exporters: [otlp/otel-fleet] }
    logs:    { exporters: [otlp/otel-fleet] }
    metrics: { exporters: [otlp/otel-fleet] }
```

For customer sites you can instead run an **edge agent** that otel-fleet manages
remotely — see [Edge agents](edge-agents.md).

### A quick test with telemetrygen

Send all three signals — `telemetrygen` needs a count (`--logs`/`--traces`/
`--metrics`) or a `--duration`, otherwise it exits with
`either 'logs' or 'duration' must be greater than 0`:

```sh
KEY='authorization="Bearer otm_ab12cd34_…"'
telemetrygen logs    --otlp-insecure --otlp-endpoint localhost:4317 --otlp-header "$KEY" --logs 20
telemetrygen traces  --otlp-insecure --otlp-endpoint localhost:4317 --otlp-header "$KEY" --traces 20
telemetrygen metrics --otlp-insecure --otlp-endpoint localhost:4317 --otlp-header "$KEY" --metrics 20
```

For a customer with a rate limit, prefer `--duration` (e.g. `--duration 30s`)
over a fixed count — on `RESOURCE_EXHAUSTED` telemetrygen retries a fixed
`--logs` count indefinitely, while `--duration` terminates cleanly.

## 4. Verify it arrived

- **Dashboard** and the **customer detail** page show ingest throughput per
  signal within a minute.
- **Explore** (pick the customer) shows the actual logs and traces you sent.
- A rejected key shows up as `refused` on the dashboard and returns a
  `RESOURCE_EXHAUSTED` / `401` to the client.

## Rate limits

A customer can carry a per-tenant quota (items/sec). Over-quota requests get a
retryable `429` / `RESOURCE_EXHAUSTED`; well-behaved SDKs and collectors retry.
Set it on the customer's **Settings** tab (or via the CLI / API).
