---
title: "SAML single sign-on"
description: "otel-fleet can act as a SAML 2.0 Service Provider (SP) for SP-initiated Web Browser SSO, alongside OIDC/OAuth and GitHub. Use it for identity providers\u2026"
---

otel-fleet can act as a **SAML 2.0 Service Provider (SP)** for SP-initiated Web
Browser SSO, alongside [OIDC/OAuth and GitHub](/otel-fleet/guides/sso/). Use it for identity
providers that speak SAML — Okta, Microsoft Entra ID, Auth0, OneLogin, Google
Workspace, ADFS.

Scope: unsigned AuthnRequests and **signed, unencrypted** assertions (the
default for the IdPs above). Assertion signatures are verified against the IdP
certificate you configure; otel-fleet holds no SP key pair. Encrypted assertions
and signed requests are not supported yet.

## Configure

Add a provider under **Settings → SSO providers → Add provider**, type
**SAML**. You need three values from your IdP:

| Field | From the IdP |
|---|---|
| IdP entity ID | the IdP's issuer / entity id |
| IdP SSO URL | the IdP's HTTP-Redirect SSO endpoint (`https://`) |
| IdP signing certificate | the IdP's signing certificate (PEM or base64 DER) |

The dialog then shows the two SP values to register **at the IdP**:

| Register at the IdP | Value |
|---|---|
| ACS URL (Assertion Consumer Service) | `https://<your-otel-fleet>/auth/<slug>/acs` |
| SP entity ID / Audience | `https://<your-otel-fleet>/auth/<slug>/metadata` |

SP metadata XML is also served at `…/auth/<slug>/metadata` for IdPs that import
it.

## Sign-in & user mapping

The provider appears on the login page as **Continue with <name>**. On success:

- **email** — the assertion `NameID` (when it is an email) or a common email
  attribute (`email`, `mail`, or the SAML/OID email claim used by Entra ID).
- **display name** — a `displayName`/`name` attribute when present.
- The user is matched/created by email (like [invites](/otel-fleet/guides/sso/)); a new user
  gets the `viewer` role. Set role and [customer access](/otel-fleet/guides/sso/) in
  **Settings → Users** (or provision users ahead of time via
  [SCIM](/otel-fleet/guides/scim/)).

The assertion's signature, validity window and audience are all verified before
a session is created.

## Configuration

- `OTEL_FLEET_BASE_URL` must be the externally reachable base URL — the ACS and
  metadata URLs the IdP posts back to are derived from it.

:::note
Encrypted assertions, signed AuthnRequests, and SAML Single Logout are not
implemented. SCIM ([provisioning](/otel-fleet/guides/scim/)) and SAML complement each other:
SCIM manages the user lifecycle, SAML logs the user in.
:::
