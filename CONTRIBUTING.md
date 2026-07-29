# Contributing to otel-fleet

Thanks for considering a contribution! otel-fleet is pre-1.0 and moving fast —
for anything larger than a bug fix, please open an issue first so we can agree
on the direction before you invest time.

## Development setup

Requirements: **Go 1.26+**, **Node 24+ with pnpm**, **Docker + Compose**.

```sh
make dev-up      # postgres, clickhouse, victoriametrics, gateway, forwarding
OTEL_FLEET_DEV_LOGIN=true OTEL_FLEET_MASTER_KEY=$(openssl rand -base64 32) make run
cd web && pnpm install && pnpm dev
```

See the [quickstart](https://sag-solutions.github.io/otel-fleet/quickstart/) for
the full walkthrough and
[development docs](https://sag-solutions.github.io/otel-fleet/development/) for
the repository layout and all make targets.

## Before you push

```sh
make gen && git diff --exit-code   # codegen drift — CI enforces this
make test                          # Go: control plane + collector components
make lint                          # golangci-lint
cd web && pnpm lint && pnpm typecheck && pnpm test
```

### The codegen drift rule

`api/openapi.yaml` and `proto/` are the contracts; generated code
(`internal/api/apigen`, `web/src/api/generated`, proto stubs) is committed.
Change a contract → run `make gen` → commit the regenerated output in the same
commit. The `codegen-drift` CI job runs exactly
`make gen && git diff --exit-code` and fails your PR otherwise.

## Where tests live

| Area | Location | Run |
| --- | --- | --- |
| Control plane (API, auth, pipelines, store, crypto, OpAMP) | `internal/**/*_test.go` | `go test ./...` |
| Collector components | `collector/extension/tenantauth`, `collector/processor/tenantstamp` | `make -C collector test` |
| Rendered-config contract | `collector/testdata/forwarding-sample.yaml` | `make -C collector validate` |
| Web UI | `web/src/**` (vitest) | `cd web && pnpm test` |
| Helm chart | lint + template (both forwarding modes) | see `.github/workflows/ci.yaml` |

New backend features need Go tests next to the code they test; pipeline
rendering/validation changes should extend the contract testdata when they
change the rendered shape.

## Commit messages: Conventional Commits

We use [Conventional Commits](https://www.conventionalcommits.org/) — they feed
the release changelog directly:

```
feat(pipelines): add kafka exporter to the catalog
fix(opamp): don't drop RemoteConfigStatus on reconnect
docs: clarify bootstrap token rotation
```

Common types: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `ci`, `chore`.
Breaking changes: `!` after the type/scope and a `BREAKING CHANGE:` footer.

## DCO sign-off

Every commit must be signed off
([Developer Certificate of Origin](https://developercertificate.org/)):

```sh
git commit -s -m "feat: ..."
```

This adds a `Signed-off-by: Your Name <you@example.com>` trailer certifying you
have the right to submit the change under the project license (Apache-2.0).

## Pull requests

- Keep PRs focused; separate refactors from behavior changes.
- Fill in the PR template — especially *how you verified it*.
- CI must be green: backend, codegen-drift, collector, web, helm, image builds.
- Security issues: **do not** open a public issue — see [SECURITY.md](SECURITY.md).
