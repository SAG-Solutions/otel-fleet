---
title: "Quickstart"
description: "Two ways to get a running otel-fleet in about five minutes:"
---

Two ways to get a running otel-fleet in about five minutes:

- **[Demo mode](#demo-mode-everything-in-containers)** — everything in containers,
  one command, no toolchain needed beyond Docker.
- **[Development mode](#development-mode-control-plane-on-the-host)** — control
  plane and web UI on the host, for hacking on otel-fleet itself.

## Demo mode (everything in containers)

Requirements: Docker with Compose v2.23.1+ (the demo file uses inline `configs`).

```sh
git clone https://github.com/sag-solutions/otel-fleet
cd otel-fleet
docker compose -f deploy/compose/docker-compose.demo.yaml up -d --build
```

This starts PostgreSQL, ClickHouse, VictoriaMetrics, vmagent, the control plane
(as a container, with the web UI and collector binary baked in), the ingest
gateway, and the forwarding tier.

:::note
The demo uses the same host ports as the development environment
(8080, 4317/4318, 4320, 5432, 8123/9000, 8428) — stop `make dev-up` /
`make run` first. The `OTEL_FLEET_MASTER_KEY` in the demo file is a
**published sample**; never reuse it outside a demo.
:::

1. Open <http://localhost:8080>. Dev login is enabled — sign in with any email
   address.
2. Create a **customer**, then create an **API key** for it. The key is shown
   **once** — copy it now.
3. Send data with the bundled load generator:

    ```sh
    OTEL_FLEET_API_KEY=otm_... docker compose \
      -f deploy/compose/docker-compose.demo.yaml --profile loadgen up loadgen
    ```

4. Watch the customer's throughput charts fill up on the dashboard and the
   customer detail page (ingest counters come straight from the gateway's
   `count` connector, labeled by `tenant.id`).

Tear down with:

```sh
docker compose -f deploy/compose/docker-compose.demo.yaml down -v
```

## Development mode (control plane on the host)

Requirements: Go 1.26+, Node 24+ with pnpm, Docker + Compose.

### 1. Start the backing services

```sh
make dev-up   # postgres, clickhouse, victoriametrics, vmagent, gateway, forwarding
```

This is `deploy/compose/docker-compose.yaml`: everything except the control plane.
The gateway resolves API keys against the control plane on the host
(`host.docker.internal:9443`), and the forwarding collector pulls its rendered
config from the host ops listener — both crash-loop harmlessly until step 2.

### 2. Run the control plane

```sh
OTEL_FLEET_DEV_LOGIN=true \
OTEL_FLEET_MASTER_KEY=$(openssl rand -base64 32) \
make run
```

- `OTEL_FLEET_DEV_LOGIN=true` enables password-less login with any email.
- `OTEL_FLEET_MASTER_KEY` (base64-encoded 32 bytes) is optional for the core loop
  but required to store SSO providers and pipeline password fields. **Persist the
  key** (e.g. in a local env file) — secrets encrypted with a lost key are
  unrecoverable.
- Pipeline validation shells out to the real collector binary; build it once with
  `make -C collector build` (expected at `collector/dist/otel-fleet-collector`,
  override with `OTEL_FLEET_OTELCOL_BIN`). Without it, validation degrades to
  structural checks.

### 3. Start the web UI

```sh
cd web && pnpm install && pnpm dev
```

Open the Vite dev server (it proxies API calls to `:8080`) and log in with any
email. To make yourself an admin, set
`OTEL_FLEET_ADMIN_EMAILS=you@example.com` in step 2.

### 4. Create a customer and send data

Create a customer and an API key in the UI (the key is shown once), then:

```sh
telemetrygen logs --otlp-insecure --otlp-endpoint localhost:4317 \
  --otlp-header 'authorization="Bearer <api-key>"'
```

(`go install github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@v0.156.0`
if you don't have it.)

### 5. Watch the charts

The dashboard and the customer's detail page show accepted log records / spans /
metric points within a scrape interval (~15s). Rows land tenant-tagged in
ClickHouse; an invalid key shows up as refused requests.

## Next steps

- [Build a forwarding pipeline](/otel-fleet/guides/pipelines/) to route a customer's data to
  an external OTLP backend.
- [Enroll an edge agent](/otel-fleet/guides/edge-agents/) with a bootstrap token.
- [Configure SSO](/otel-fleet/guides/sso/) and invite your team.
- [Deploy on Kubernetes](/otel-fleet/installation/helm/).
