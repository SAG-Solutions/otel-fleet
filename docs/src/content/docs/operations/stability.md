---
title: "Stability & versioning"
description: "What the SemVer contract covers from 1.0 onward, what it deliberately excludes, and the deprecation policy."
---

From **v1.0.0**, otel-fleet follows [Semantic Versioning](https://semver.org/).
This page is the contract: what's stable, what isn't, and how changes are made.

## What SemVer covers

Backward-incompatible changes to these surfaces require a **major** version bump:

- **REST API under `/api/v1`** — paths, operation IDs, request/response schemas
  and their field names, and the shared error shape (`{code, message}`). New
  optional fields and new endpoints are **minor** (additive); list responses are
  object-wrapped (`{items: [...]}`) so pagination/metadata can be added without
  breaking.
- **The `OTEL_FLEET_` environment surface** — variable names and their meaning.
- **Helm chart values** for `otel-fleet` and `otel-fleet-agent` — the documented
  keys in `values.yaml`.

**Minor** = backward-compatible additions (new endpoints/fields/env/values,
new features). **Patch** = fixes with no interface change.

## What SemVer does NOT cover

These are internal or data-plane contracts, versioned on their own cadence — do
not build integrations that depend on their stability:

- **Internal gRPC `AuthService`** (proto package `otelfleet.auth.v1`) — the
  control-plane↔gateway call; its own `.v1` is independent of the REST `/api/v1`.
- **ClickHouse schema** (`deploy/clickhouse/schema/`) — telemetry storage layout;
  TTL-bounded and reconstructable.
- **PostgreSQL control-plane schema** and the advisory-lock leader election —
  internal state (backed up, not a public interface).
- **Prometheus/self-telemetry metric names** (`otel_fleet_*`) — operational
  signals; we avoid churn but do not guarantee it. Pin dashboards/alerts to a
  release if you need stability.
- **Collector distro component set** (`collector/builder-config*.yaml`) — the
  bundled receivers/processors/exporters track upstream.
- **OpAMP wire surface** (endpoint, `AgentDescription`/`ComponentHealth` payloads,
  the enrollment/token exchange) — governed by upstream OpAMP.
- **Deployment identity defaults** — the `otelfleet`/`otel` DB user/name literals
  and the session cookie name.

## Documented conventions (stable, but worth knowing)

Deliberate choices frozen at 1.0 that could otherwise surprise:

- **`Customer.tenantId`** is the customer's tenant identifier (the `tenant.id`
  resource-attribute stamp, `cust_…`). The unrelated `AuthProviderConfig.clientId`
  is the OAuth/OIDC client id — different concept, different schema.
- **IDs are UUID strings** except `AuditEntry.id` and `AgentEvent.id`, which are
  `int64` (database sequences). Don't assume every `id` is a string.
- The scoped-metrics paths use **`query_range`** (snake_case) to mirror the
  Prometheus/VictoriaMetrics API they proxy — the one intentional exception to
  kebab-case paths.
- **`OTEL_FLEET_SESSION_SECURE` defaults to `false`** so the binary works over
  plain HTTP for local/dev; set it `true` in production (the Helm chart does).
  This is a *default*, not a frozen guarantee — see the
  [security model](/operations/security-model/#deployment-responsibilities).

## Deprecation policy

When a covered surface must change incompatibly, we deprecate before removing:
the old form keeps working for at least one minor release, is marked deprecated
in the OpenAPI spec / values comments and the CHANGELOG, and is removed no sooner
than the next major. Security fixes may move faster (see `SECURITY.md`).

## Supported versions

Per `SECURITY.md`, the **latest minor** receives fixes. Upgrade within a major
line freely (backward compatible); read the CHANGELOG's upgrade notes when moving
across a major.
