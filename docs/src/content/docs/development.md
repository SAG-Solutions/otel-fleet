---
title: "Development"
description: "api/openapi.yaml REST contract \u2014 source of truth for Go + TS codegen cmd/otel-fleet control-plane binary (one process: REST/SPA, gRPC, ops, OpAMP)\u2026"
---

## Repository layout

```
api/openapi.yaml     REST contract — source of truth for Go + TS codegen
cmd/otel-fleet        control-plane binary (one process: REST/SPA, gRPC, ops, OpAMP)
internal/            backend packages (api, auth, authz, config, crypto, opamp,
                     pipelines, store, tenants, …)
proto/               internal gRPC contract (API-key validation, buf-managed)
collector/           custom collector distro: OCB manifest + local components
                     (extension/tenantauth, processor/tenantstamp)
web/                 React SPA (Vite, TanStack Router/Query, generated client)
deploy/compose/      dev + demo environments
deploy/charts/       Helm chart
deploy/clickhouse/   ClickHouse DDL (owned here, exporter runs create_schema:false)
docs/                this site (MkDocs Material)
```

## Prerequisites

Go 1.26+, Node 24+ with pnpm, Docker + Compose. For the docs:
[uv](https://docs.astral.sh/uv/) (`make docs-serve` uses `uvx`).

## Make targets

| Target | What it does |
| --- | --- |
| `make dev-up` / `make dev-down` | Compose dev environment up (with build) / down incl. volumes |
| `make run` | `go run ./cmd/otel-fleet` (control plane on the host) |
| `make build` | Build `bin/otel-fleet` |
| `make test` | Go tests: control plane + both collector components |
| `make lint` | `golangci-lint run` |
| `make gen` | All codegen (`gen-go` + `gen-web`) |
| `make gen-go` | oapi-codegen from `api/openapi.yaml` + buf for `proto/` |
| `make gen-web` | Regenerate the TS client (`cd web && pnpm gen`) |
| `make -C collector build/test/validate/docker/proto` | Collector distro tasks (see `collector/README.md`) |
| `make docs-serve` / `make docs-build` | MkDocs live preview / strict build (needs uv) |

Web workflow: `cd web && pnpm install && pnpm dev` (proxies API calls to
`:8080`); `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm build`.

## The codegen drift rule

`api/openapi.yaml` and `proto/` are contracts; generated code is committed.
**Any change to a contract must be committed together with its regenerated
output**, and CI enforces it:

```sh
make gen && git diff --exit-code
```

If that fails locally, your PR will fail the `codegen-drift` job.

## Tests by area

| Area | Where | Run with |
| --- | --- | --- |
| Control plane | `internal/**/*_test.go` | `go test ./...` |
| Collector components | `collector/extension/tenantauth`, `collector/processor/tenantstamp` | `make -C collector test` |
| Rendered-config contract | `collector/testdata/forwarding-sample.yaml` | `make -C collector validate` |
| Web | `web/src/**` (vitest) | `cd web && pnpm test` |
| Helm | lint + template of both forwarding modes | see `.github/workflows/ci.yaml` |

Pipeline-validation tests exercise the real distro binary; build it first with
`make -C collector build` (tests that need it degrade or skip when it is
missing, mirroring the server's behavior).

## Conventions that matter here

- **OpenAPI-first**: add/modify endpoints in `api/openapi.yaml` first, then
  `make gen`, then implement the generated server interface.
- **Migrations**: add a new numbered file under `internal/store/migrations/`;
  they run automatically at startup (goose).
- **ClickHouse DDL** is owned by `deploy/clickhouse/schema/` — when bumping the
  collector release train, re-verify it against the pinned exporter's insert
  statements (see `collector/README.md`, "Version bumps").
- **Collector release train** (core/contrib/OCB/supervisor) is pinned at
  v0.156.0 and bumped in lockstep across `collector/Makefile`,
  `builder-config.yaml`, both Dockerfiles and the component go.mods.
- Conventional commits, DCO sign-off — see
  [CONTRIBUTING.md](https://github.com/sag-solutions/otel-fleet/blob/main/CONTRIBUTING.md).

## Docs site

```sh
make docs-serve   # live preview at :8000
make docs-build   # strict build (what CI runs)
```

Both use `uvx --with mkdocs-material mkdocs …`, so the only local dependency is
[uv](https://docs.astral.sh/uv/).
