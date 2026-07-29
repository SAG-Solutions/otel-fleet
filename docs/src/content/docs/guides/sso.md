---
title: "Single sign-on"
description: "otel-fleet authenticates UI users via external identity providers. Providers are managed by admins in the UI (Settings \u2192 SSO) or via\u2026"
---

otel-fleet authenticates UI users via external identity providers. Providers are
managed by admins in the UI (Settings → SSO) or via
`/api/v1/settings/auth-providers`, stored in PostgreSQL with the client secret
encrypted at rest.

:::note[Prerequisites]
- `OTEL_FLEET_MASTER_KEY` must be configured — client secrets are
  AES-256-GCM-encrypted with it; saving a provider fails without it.
- `OTEL_FLEET_BASE_URL` must be the externally visible URL — redirect URIs
  are derived from it.
:::

## Provider types

| Type | Protocol | Issuer | Notes |
| --- | --- | --- | --- |
| `google` | OIDC | fixed: `https://accounts.google.com` | |
| `microsoft` | OIDC | fixed: `https://login.microsoftonline.com/common/v2.0` (multi-tenant Entra ID) | issuer validation relaxed **by design**, see below |
| `github` | OAuth2 (not OIDC) | — | scopes `read:user user:email` |
| `oidc` | OIDC | you provide it (**`https://` required**) | for Keycloak, Okta, Authentik, Dex, … |

Each provider gets a unique URL slug (`name`, lowercase `[a-z0-9-]`); the login
flow lives at `/auth/{name}/start` and the **redirect/callback URI to register
at the IdP** is:

```
{OTEL_FLEET_BASE_URL}/auth/{name}/callback
```

e.g. `https://otel-fleet.example.com/auth/google/callback`.

### Microsoft: why issuer validation is relaxed

The multi-tenant Entra ID endpoint's discovery document advertises the literal
`{tenantid}` issuer template, and ID tokens carry per-tenant issuers — so strict
OIDC issuer matching is impossible against the `common` endpoint. otel-fleet
therefore relaxes issuer validation **for the `microsoft` type only** and
validates tokens per tenant at login. The provider **Test** button explains this
when it detects the template issuer.

### GitHub

GitHub is plain OAuth2 (no ID token): otel-fleet requests `read:user` and
`user:email` and resolves the user's primary verified email via the GitHub API.

### Generic OIDC

Any spec-compliant provider works. The issuer must be an `https://` URL and its
discovery document (`/.well-known/openid-configuration`) must be reachable from
the control plane — use **Test** (`POST .../auth-providers/{id}/test`) to check
discovery and issuer match without leaking secrets.

## Setting up a provider (example: Google)

1. Create an OAuth client at the IdP; register the redirect URI
   `{BASE_URL}/auth/google/callback`.
2. In otel-fleet: Settings → SSO → add provider — type `google`, name `google`,
   display name, client ID + secret. The secret is encrypted at rest and never
   returned by the API (updates that omit it keep the stored one).
3. **Test** the provider, then enable it. It appears as a button on the login
   page immediately.

## Users, roles, invites

Roles, weakest to strongest: **`viewer`** (read-only) → **`operator`** (can
manage customers, keys, pipelines, agents) → **`admin`** (everything, including
users, SSO settings and the audit log).

- Emails in `OTEL_FLEET_ADMIN_EMAILS` get `admin` on login; everyone else defaults
  to `viewer`.
- **Invites** (`POST /api/v1/users`, admin-only): pre-create a user by email with
  a role; it applies on their first SSO login through any provider.
- Admins can change roles, disable/enable and delete users. Guardrails: no
  self-demotion, and the last active admin can be neither disabled nor deleted.
- User identities are keyed per provider type (and per issuer for custom OIDC
  providers), matched by verified email into a single account.

## Audit log

Admin actions and security-relevant events (customer/key/pipeline/user/provider
changes, …) land in the audit log: `GET /api/v1/audit`, filterable by action,
entity type, customer, actor and time range, cursor-paged (`beforeId`), newest
first.

## Environment fallback provider

A single generic OIDC provider can be configured via `OTEL_FLEET_OIDC_*`
environment variables (useful for bootstrap, before any admin exists) — see the
[configuration reference](/otel-fleet/installation/configuration/#environment-defined-oidc-provider-bootstrap-fallback).
It shows up under the name `oidc`; a database provider with the same name shadows
it.
