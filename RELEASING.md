# Releasing otel-fleet

Releases are **automated** with [release-please](https://github.com/googleapis/release-please)
driven by [Conventional Commits](https://www.conventionalcommits.org/). You no
longer tag or bump versions by hand — you merge a PR. One version number covers
everything: the binary, the three container images and both Helm charts.

## How it works

`.github/workflows/release-please.yaml` runs on every push to `main`:

1. **release-please** looks at the Conventional Commits since the last release
   and keeps an open **release PR** titled `chore(main): release X.Y.Z`. That PR
   bumps `.release-please-manifest.json`, updates `CHANGELOG.md`, and bumps
   `version`/`appVersion` in both `deploy/charts/*/Chart.yaml` (the
   `# x-release-please-version` annotations, listed as `extra-files`).
2. When you **merge the release PR**, release-please creates the `vX.Y.Z` tag
   and the GitHub Release (notes = the changelog).
3. In the **same workflow run** (so no PAT is needed to trigger it), three jobs
   then build and publish, gated on `release_created`:
   - `goreleaser` — appends the `otel-fleet` binaries + checksums to the release.
   - `images` — builds/pushes the three multi-arch images to GHCR.
   - `helm-chart` — packages/pushes both charts to GHCR.

Version bumps (pre-1.0): `fix:` → patch, `feat:` → minor, `feat!:`/`BREAKING
CHANGE:` → minor (kept minor while `0.x` via `bump-minor-pre-major`).

## What a release produces

| Artifact | Where | Built by |
| --- | --- | --- |
| `otel-fleet` binaries (linux/darwin × amd64/arm64) + checksums | GitHub Releases | `goreleaser` job (`.goreleaser.yaml`, `mode: append`) |
| `ghcr.io/sag-solutions/otel-fleet:{X.Y.Z, latest}` (control plane) | GHCR | `images` job, `Dockerfile` |
| `ghcr.io/sag-solutions/otel-fleet-collector:{X.Y.Z, latest}` | GHCR | `images` job, `Dockerfile.collector` |
| `ghcr.io/sag-solutions/otel-fleet-supervisor:{X.Y.Z, latest}` | GHCR | `images` job, `Dockerfile.supervisor` |
| Helm charts `oci://ghcr.io/sag-solutions/charts/{otel-fleet,otel-fleet-agent}:X.Y.Z` | GHCR | `helm-chart` job |

Images (multi-arch) and the charts are signed with cosign keyless (GitHub
OIDC). Verify with:

```sh
cosign verify ghcr.io/sag-solutions/otel-fleet:X.Y.Z \
  --certificate-identity-regexp 'https://github.com/sag-solutions/otel-fleet/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Cutting a release

1. Land your changes on `main` with Conventional Commit messages.
2. release-please opens/updates the **release PR** automatically. Review the
   proposed version + changelog.
3. **Merge the release PR.** Everything else (tag, GitHub Release, binaries,
   images, charts) happens automatically in that workflow run — watch it in the
   Actions tab.

### One-time setup

- Repo/org setting **Settings → Actions → General → Workflow permissions →
  "Allow GitHub Actions to create and approve pull requests"** must be enabled,
  otherwise release-please cannot open its PR with the default `GITHUB_TOKEN`.

### Sanity-check after a release

```sh
docker pull ghcr.io/sag-solutions/otel-fleet:X.Y.Z
helm show chart oci://ghcr.io/sag-solutions/charts/otel-fleet --version X.Y.Z
```

## Versioning policy (pre-1.0)

- Semver-ish: breaking changes bump the **minor** version while we're `0.x`.
- `latest` image tags always point at the newest release; deployments should
  pin versions.
- Only the latest minor release receives fixes (see `SECURITY.md`).

## If a release goes wrong

- **A build job failed after the release was created:** fix the problem on
  `main`; the fix commit lets release-please open a new patch release PR. GHCR
  tags from a partial run are overwritten on the next release. (You can also
  re-run the failed jobs from the Actions tab against the existing tag.)
- **Bad release already published:** do not delete published artifacts people
  may already pull; merge a `fix:` and let release-please cut a patch.
- **Need a manual release:** re-enable a tag trigger or run goreleaser locally;
  the automated path is strongly preferred.
