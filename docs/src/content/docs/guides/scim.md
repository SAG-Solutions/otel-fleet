---
title: "SCIM provisioning"
description: "otel-fleet exposes a SCIM 2.0 (RFC 7643/7644) Users endpoint so an identity provider (Okta, Microsoft Entra ID, OneLogin, \u2026) can create, update and\u2026"
---

otel-fleet exposes a **SCIM 2.0** (RFC 7643/7644) `Users` endpoint so an
identity provider (Okta, Microsoft Entra ID, OneLogin, …) can create, update
and deprovision console users automatically — no manual invites.

SCIM manages the user **lifecycle**, and — via **Groups** — can also drive each
user's **role and tenant (customer) access** from IdP group membership (see
[Group mapping](#group-mapping-role--tenant)). Users not governed by a mapped
group get the configured default role (`viewer`, least privilege) and are
managed by an admin in **Settings → Users**. This pairs with
[SSO](/otel-fleet/guides/sso/): SCIM allow-lists and deprovisions the account,
SSO logs the user in.

## Endpoint & authentication

| | |
|---|---|
| Base URL | `https://<your-otel-fleet>/scim/v2` |
| Auth | `Authorization: Bearer <admin API token>` |
| Content type | `application/scim+json` |

Create an **admin** management-API token under **Settings → API tokens** and
paste it into your IdP's SCIM configuration as the bearer token. SCIM requires
the `admin` role; operator/viewer tokens get `403`.

Discovery endpoints (`/ServiceProviderConfig`, `/ResourceTypes`, `/Schemas`)
are served for IdP auto-configuration.

## Attribute mapping

| SCIM | otel-fleet |
|---|---|
| `userName` (or primary `emails[]`) | email (the account key) |
| `active` | enabled / disabled |
| `displayName` / `name.formatted` | display name |
| `externalId` | stored for the IdP's reconciliation |
| group membership | role + tenant access, by convention (see below) |

`userName` (email) is the account identity and is **not** changed by SCIM
updates; `displayName`, `externalId` and `active` are.

## Lifecycle

- **Create** — `POST /Users`. Returns `201`; `409` if the userName already
  exists (the IdP then reconciles with a filter and patches).
- **Reconcile** — `GET /Users?filter=userName eq "user@example.com"`.
- **Update** — `PUT`/`PATCH /Users/{id}` (e.g. `active`, `displayName`).
- **Deprovision** — `PATCH active:false` or `DELETE /Users/{id}` **disables**
  the account (and kills its sessions) rather than hard-deleting, preserving
  the audit trail. Deactivating the last enabled admin is refused (`409`).

## Quick test with curl

```sh
TOKEN=otm_pat_…            # an admin API token
BASE=https://<your-otel-fleet>/scim/v2

# provision
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/scim+json' \
  -X POST "$BASE/Users" -d '{
    "schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
    "userName":"alice@example.com","displayName":"Alice","active":true
  }'

# reconcile
curl -G -H "Authorization: Bearer $TOKEN" "$BASE/Users" \
  --data-urlencode 'filter=userName eq "alice@example.com"'

# deprovision
curl -H "Authorization: Bearer $TOKEN" -X DELETE "$BASE/Users/<id>"
```

## Group mapping (role + tenant)

Push **SCIM `Groups`** from your IdP and their names drive each member's role
and customer access — by naming convention:

| Group `displayName` | Effect on members |
|---|---|
| `role:admin` / `role:operator` / `role:viewer` | sets the role (highest wins across groups) |
| `customer:<slug>` | grants access to that customer (by [customer slug](/otel-fleet/guides/multi-tenancy/)) |

The prefixes are configurable (`OTEL_FLEET_SCIM_GROUP_ROLE_PREFIX`,
`OTEL_FLEET_SCIM_GROUP_CUSTOMER_PREFIX`).

Mapping is **authoritative** for any user who is a member of at least one mapped
group:

- **role** = the highest `role:` group among their groups, or the default role
  if they have a mapped group but no `role:` group;
- **tenant access** = the union of their `customer:<slug>` groups. Removing a
  user from a `customer:` group revokes that access on the next change.
- A managed user with **no** `customer:` group has access to **no** customers
  (not all) — the opposite of the manual default, so group membership can't
  accidentally widen access. Unknown slugs are skipped.
- The **last enabled admin** is never demoted by SCIM (the role stays `admin`).

Users who have never been in a mapped group are unaffected and stay
manually managed in **Settings → Users**.

`Groups` support the same CRUD + `PATCH` (add/remove/replace members) that Okta
and Entra use; reconcile with `GET /Groups?filter=displayName eq "role:admin"`.

## Configuration

- `OTEL_FLEET_SCIM_DEFAULT_ROLE` — role for newly provisioned users and for
  managed users with no `role:` group (default `viewer`; `operator`/`admin` also
  accepted).
- `OTEL_FLEET_SCIM_GROUP_ROLE_PREFIX` — group-name prefix for role mapping
  (default `role:`).
- `OTEL_FLEET_SCIM_GROUP_CUSTOMER_PREFIX` — group-name prefix for customer
  mapping (default `customer:`).
