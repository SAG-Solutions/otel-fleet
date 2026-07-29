# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities **privately** to
**security@sag-solutions.com**. Do not open public GitHub issues for security
problems.

Include what you can: affected component (control plane, collector distro,
supervisor image, Helm chart), a reproduction or PoC, impact assessment, and
your environment. You'll receive an acknowledgment within **3 business days**.

## Coordinated disclosure

We follow a **90-day** coordinated disclosure window: we ask you to keep the
issue private for up to 90 days from your report (or until a fix is released,
whichever comes first). We'll keep you informed of progress, coordinate the
disclosure date with you, and credit you in the advisory unless you prefer
otherwise.

## Supported versions

otel-fleet is pre-1.0: **only the latest minor release** receives security
fixes. Older releases will not be patched — upgrade to the newest `0.x` to
stay supported.

| Version | Supported |
| --- | --- |
| latest `0.x` minor | ✔ |
| anything older | ✘ |

## Deployment hardening notes

Things the docs call out that deserve repeating here:

- The internal listeners (gRPC `:9443`, ops `:9090`, OpAMP `:4320`) are
  **plaintext** — never expose them directly; put OpAMP behind TLS termination
  for edge agents.
- `OTEL_FLEET_DEV_LOGIN=true` disables authentication in favor of a dev login —
  local use only.
- `OTEL_FLEET_MASTER_KEY` protects secrets at rest (SSO client secrets,
  pipeline credentials); store it in a secret manager and never reuse the
  published demo key.
