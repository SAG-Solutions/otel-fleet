# SCIM provisioning

otel-fleet exposes a **SCIM 2.0** (RFC 7643/7644) `Users` endpoint so an
identity provider (Okta, Microsoft Entra ID, OneLogin, …) can create, update
and deprovision console users automatically — no manual invites.

SCIM manages the user **lifecycle**; it does not set roles or tenant scope. A
provisioned user gets the configured default role (`viewer`, least privilege);
an admin then sets the role and [customer access](sso.md) in
**Settings → Users**. This pairs with [SSO](sso.md): SCIM allow-lists and
deprovisions the account, SSO logs the user in.

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
| role / groups | **not** mapped — default `viewer`, changed in the UI |

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

## Configuration

- `OTEL_FLEET_SCIM_DEFAULT_ROLE` — role for newly provisioned users
  (default `viewer`; `operator`/`admin` also accepted).

!!! note
    Group-based role/tenant-scope mapping (SCIM `Groups`) is not yet
    implemented — assign role and customer access per user in the UI.
