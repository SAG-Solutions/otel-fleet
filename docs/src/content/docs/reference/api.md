---
title: "REST API"
description: "The API is OpenAPI-first: api/openapi.yaml is the source of truth, and both the Go server interfaces and the TypeScript client are generated from it (make\u2026"
---

The API is **OpenAPI-first**: [`api/openapi.yaml`](https://github.com/sag-solutions/otel-fleet/blob/main/api/openapi.yaml)
is the source of truth, and both the Go server interfaces and the TypeScript
client are generated from it (`make gen`). The spec is the authoritative,
complete reference — this page is a tour.

**Conventions.** Base path `/api/v1`. Session-cookie authentication (log in via
SSO or dev login). Errors are a JSON `{code, message}` object. Roles:
`viewer` < `operator` < `admin`; mutating endpoints need `operator`, the
admin section needs `admin`.

## Auth and session

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/me` | Current user + role |
| `GET /api/v1/auth/providers` | Login providers to render on the login page |
| `POST /api/v1/auth/dev-login` | Password-less login (only with `OTEL_FLEET_DEV_LOGIN=true`) |
| `POST /api/v1/auth/logout` | Destroy the session |

Browser SSO flows live outside the API prefix: `GET /auth/{name}/start` and
`GET /auth/{name}/callback`.

## Customers and API keys

| Endpoint | Purpose |
| --- | --- |
| `GET`/`POST /api/v1/customers` | List / create customers |
| `GET`/`PATCH`/`DELETE /api/v1/customers/{id}` | Manage one customer |
| `GET`/`POST /api/v1/customers/{id}/api-keys` | List / create ingest API keys — the secret appears **only** in the create response |
| `DELETE /api/v1/customers/{id}/api-keys/{keyId}` | Revoke (propagates to gateways within the 30s auth cache) |

## Stats

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/stats/overview` | Fleet-wide totals for the dashboard |
| `GET /api/v1/customers/{id}/stats/throughput` | Per-customer accepted/refused time series |
| `GET /api/v1/pipelines/{id}/stats/stages` | Per-stage received / sent / failed / queued |

## Pipelines

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/catalog/components` | Component catalog incl. JSON Schemas (drives the builder UI) |
| `GET /api/v1/pipelines` · `GET`/`POST /api/v1/customers/{id}/pipelines` | List / create (target class `forwarding` or `edge`) |
| `POST /api/v1/pipelines/validate` | Validate a graph without saving (structural + real `otelcol validate`) |
| `GET`/`PATCH`/`DELETE /api/v1/pipelines/{id}` | Manage one pipeline |
| `GET`/`POST /api/v1/pipelines/{id}/versions` | Version history / save a new version |
| `GET /api/v1/pipelines/{id}/versions/{v}` | One version incl. rendered fragment |
| `POST /api/v1/pipelines/{id}/versions/{v}/activate` | Activate (also how rollback works) |

## Fleet (agents and enrollment)

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/agents` | Gateway replicas and edge agents (`?class=gateway\|edge`) |
| `GET`/`DELETE /api/v1/agents/{id}` | Detail / remove (409 while connected) |
| `GET /api/v1/agents/{id}/config` | Assigned vs. reported config, with diff |
| `GET /api/v1/agents/{id}/events` | Lifecycle event timeline |
| `GET`/`POST /api/v1/customers/{id}/bootstrap-tokens` | Enrollment tokens — secret is **show-once** |
| `DELETE /api/v1/customers/{id}/bootstrap-tokens/{tokenId}` | Revoke a token |

## Admin

| Endpoint | Purpose |
| --- | --- |
| `GET`/`POST /api/v1/users` | List users / invite by email + role (applies on first SSO login) |
| `PATCH`/`DELETE /api/v1/users/{id}` | Role changes, disable/enable, delete — the last active admin is protected |
| `GET`/`POST /api/v1/settings/auth-providers` | SSO providers (secrets never returned) |
| `PATCH`/`DELETE /api/v1/settings/auth-providers/{id}` | Update (omit `clientSecret` to keep it) / remove |
| `POST /api/v1/settings/auth-providers/{id}/test` | Connectivity test (OIDC discovery / GitHub reachability) |
| `GET /api/v1/audit` | Audit log — filter by action, entity type, customer, actor, time range; cursor-paged via `beforeId` |

## Non-REST surfaces

Not part of the OpenAPI contract, but part of the system's API surface:

- **gRPC** `otelfleet.auth.v1.AuthService` (`proto/authservice.proto`) on
  `:9443` — API-key validation for `tenantauth`.
- **OpAMP** WebSocket on `:4320/v1/opamp` — edge-agent management.
- **Ops HTTP** on `:9090` — `/metrics`, `/healthz`, `/readyz`,
  `GET /internal/v1/collector-config/forwarding` (consumed by the forwarding
  collector's HTTP config provider).
