# Tenant self-service portal

otelfleet has two faces of the same console:

- **Admin console** — what administrators and unscoped operators see: the whole
  fleet, every customer, settings, billing and the audit log.
- **Self-service portal** — what a *tenant-scoped* user sees: only their own
  customer(s), with everything they need to run their telemetry themselves.

There is no separate deployment or login for the portal. The UI adapts to the
signed-in user: a non-admin who is granted one or more customers automatically
gets the portal instead of the admin console.

## What a portal user can do

Within their own tenant, a portal user with the **operator** role can:

- see their ingest throughput (logs / traces / metrics) and usage & cost;
- create and revoke their own **API keys** (show-once secrets);
- **enroll edge agents** using a bootstrap token, and see agent status;
- view, edit, validate and **deploy their own pipelines**.

A portal user with the **viewer** role sees the same pages read-only.

Portal users can **never** reach fleet-wide or global surfaces — other
customers, Settings (SSO/SCIM/webhooks/alert rules), billing pricing, the audit
log or user management. This is enforced by the API (every tenant-scoped
endpoint checks the caller's grants and returns `403` otherwise), not just
hidden in the UI.

## Creating a portal user (admin)

1. **Settings → Users → Invite user** (or let them sign in via SSO first).
2. Give them the **operator** role (for full self-service) or **viewer**
   (read-only).
3. **Grant exactly one customer**: open the user's access and select the single
   customer they belong to.

That's it — the grant is what turns a normal user into a scoped portal user.
The rule is:

| Role | Grants | Experience |
|---|---|---|
| admin | (ignored) | Admin console, all customers |
| operator / viewer | none | Admin console, all customers (backward compatible) |
| operator / viewer | ≥ 1 customer | **Self-service portal**, limited to those customers |

Granting more than one customer is supported — the portal shows a customer
switcher — but the typical case is one customer per external user.

## How it works

The `/api/v1/me` response carries `role`, `allCustomers` and
`scopedCustomerIds`. The SPA treats a session as a portal session when the user
is a non-admin with `allCustomers = false` and at least one scoped customer,
and renders the portal navigation and a tenant overview instead of the fleet
dashboard. Every data call is the same scoped API the admin console uses; the
server filters results and rejects out-of-scope access, so the portal cannot
see or touch another tenant even by crafting requests directly.
