# CLI & config-as-code

`otel-fleetctl` manages customers and pipelines from the command line and lets
you treat them as **declarative config** in Git (GitOps). It talks to the
control-plane REST API with a management-API token.

## Install

Grab `otel-fleetctl` from a [release](https://github.com/sag-solutions/otel-fleet/releases)
(linux/darwin/windows × amd64/arm64), or build it:

```sh
make cli        # → bin/otel-fleetctl
```

## Authenticate

Create a **management-API token** (distinct from ingest API keys) under
**Settings → API tokens** (admin only). A token has its own role
(admin/operator/viewer) and looks like `otm_pat_…`. Point the CLI at your
control plane:

```sh
export OTEL_FLEET_URL=https://otel-fleet.example.com
export OTEL_FLEET_TOKEN=otm_pat_…
otel-fleetctl customers
```

The token authenticates as `Authorization: Bearer <token>`; it is not a browser
session, so it is exempt from CSRF but bound by its role (an `operator` token
cannot touch admin-only areas).

## Commands

```
otel-fleetctl customers            # list customers
otel-fleetctl pipelines            # list pipelines (all customers)
otel-fleetctl export -o fleet.yaml # dump customers + pipelines as declarative YAML
otel-fleetctl apply -f fleet.yaml  # reconcile the target from the file
otel-fleetctl apply -f fleet.yaml --dry-run   # print the plan, change nothing
```

## Config-as-code

`export` writes a declarative spec; commit it to Git. `apply` reconciles the
target to match it — idempotent, so re-running a clean spec reports
`everything up to date`.

```yaml
customers:
  - name: ACME Corp
    slug: acme-corp
    rateLimitItemsPerSec: 500      # optional quota
    retentionDays: 14              # optional retention override
    pipelines:
      - name: Forward Logs
        targetClass: forwarding    # or edge
        graph:
          signals: [logs]
          processors:
            - type: batch
              config: { send_batch_size: 1024 }
          exporters:
            - type: otlphttp
              config:
                endpoint: https://backend.acme.example.com
```

`apply` reconciliation:

- **Customers** are matched by `slug` (or `name` when no slug is given):
  created if missing, updated for name / quota / retention.
- **Pipelines** are matched by `name` within the customer: created + activated
  if missing; when the graph differs from the active version, a new version is
  appended and activated (rollback stays available in the UI); unchanged
  pipelines are skipped.

Run `apply --dry-run` in CI on a pull request to preview the plan, and `apply`
on merge to roll it out.

### Secrets caveat

Exporter credentials (e.g. `headers.authorization`) are **redacted**
(`__otel_fleet_redacted__`) in `export` output — never commit real secrets to
Git. When applying to a target that already has the pipeline, the stored secret
is kept automatically. For a **fresh** target, fill the secret in before the
first apply (from your secret manager / CI variable), or set it once in the UI.
