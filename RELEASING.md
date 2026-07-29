# Releasing otel-fleet

Releases are cut from `main` by pushing a `v*` tag. One version number covers
everything: the binary, the three container images and the Helm chart.

## What a release produces

| Artifact | Where | Built by |
| --- | --- | --- |
| `otel-fleet` binaries (linux/darwin × amd64/arm64) + checksums + GitHub release with conventional-commit changelog | GitHub Releases | `goreleaser` job (`.goreleaser.yaml`) |
| `ghcr.io/sag-solutions/otel-fleet:{X.Y.Z, latest}` (control plane) | GHCR | `images` job, `Dockerfile` |
| `ghcr.io/sag-solutions/otel-fleet-collector:{X.Y.Z, latest}` | GHCR | `images` job, `Dockerfile.collector` |
| `ghcr.io/sag-solutions/otel-fleet-supervisor:{X.Y.Z, latest}` | GHCR | `images` job, `Dockerfile.supervisor` |
| Helm chart `oci://ghcr.io/sag-solutions/charts/otel-fleet:X.Y.Z` | GHCR | `helm-chart` job |

Images (multi-arch, linux/amd64 + linux/arm64) and the chart are signed with
cosign keyless (GitHub OIDC). Verify with:

```sh
cosign verify ghcr.io/sag-solutions/otel-fleet:X.Y.Z \
  --certificate-identity-regexp 'https://github.com/sag-solutions/otel-fleet/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Cutting a release

### 1. Bump the chart version (manual, required)

The chart version is **not** derived from the tag — bump it by hand and the
release workflow fails fast if you forget:

```sh
# deploy/charts/otel-fleet/Chart.yaml
version: X.Y.Z      # must equal the tag without the leading v
appVersion: "X.Y.Z" # image tag the chart defaults to
```

Commit via a normal PR (conventional commit, e.g.
`chore(release): v0.4.0`), get it green and merge.

### 2. Tag and push

```sh
git checkout main && git pull
git tag vX.Y.Z
git push origin vX.Y.Z
```

That's it — `.github/workflows/release.yaml` does the rest. Watch the three
jobs (`goreleaser`, `images`, `helm-chart`) in the Actions tab.

### 3. Sanity-check

```sh
docker pull ghcr.io/sag-solutions/otel-fleet:X.Y.Z
helm show chart oci://ghcr.io/sag-solutions/charts/otel-fleet --version X.Y.Z
```

Skim the generated release notes; edit the GitHub release text if the
changelog grouped something oddly.

## Versioning policy (pre-1.0)

- Semver-ish: breaking changes bump the **minor** version while we're `0.x`.
- `latest` image tags always point at the newest release; deployments should
  pin versions.
- Only the latest minor release receives fixes (see `SECURITY.md`).

## If a release goes wrong

- **Workflow failed after the tag pushed:** fix the problem on `main`, delete
  the tag (`git push origin :refs/tags/vX.Y.Z`, delete the draft/failed GitHub
  release if any), re-tag. GHCR tags already pushed will be overwritten on the
  re-run.
- **Bad release already published:** do not delete published artifacts people
  may already pull; cut a patch release instead.
