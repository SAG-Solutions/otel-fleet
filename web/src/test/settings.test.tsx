import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderApp, stubApi, testViewerMe } from '@/test/render-app'
import { setCsrfToken } from '@/lib/api-client'

beforeEach(() => {
  stubApi()
})

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('/settings?tab=sso', () => {
  it('renders the provider table with source chips and env read-only rows', async () => {
    renderApp('/settings?tab=sso')
    expect(await screen.findByText('Google Workspace')).toBeInTheDocument()
    // Slug + truncated client id in mono.
    expect(screen.getByText('google')).toBeInTheDocument()
    expect(screen.getByText('1234567890-abc.apps.googleusercontent.com')).toBeInTheDocument()
    // Source chips.
    expect(screen.getAllByText('database').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('env')).toBeInTheDocument()
    // Database provider row has actions; env provider switch is read-only.
    expect(screen.getByRole('button', { name: 'Edit Google Workspace' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete Google Workspace' })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Corp Login enabled' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Edit Corp Login' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /add provider/i })).toBeInTheDocument()
  })

  it('renders a SAML provider with its type badge and actions', async () => {
    renderApp('/settings?tab=sso')
    // SAML row renders with display name, SAML type badge, and its IdP entity id
    // (shown in the client-id column since SAML has no client id).
    expect(await screen.findByText('Okta SAML')).toBeInTheDocument()
    expect(screen.getByText('SAML')).toBeInTheDocument()
    expect(screen.getByText('https://idp.okta.example.com/entity')).toBeInTheDocument()
    // Database-backed SAML row keeps the standard edit/delete actions.
    expect(screen.getByRole('button', { name: 'Edit Okta SAML' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete Okta SAML' })).toBeInTheDocument()
  })

  it('shows IdP fields and SP registration details when SAML is selected in the create dialog', async () => {
    const user = userEvent.setup()
    renderApp('/settings?tab=sso')
    await user.click(await screen.findByRole('button', { name: /add provider/i }))
    const dialog = await screen.findByRole('dialog')
    // Defaults to Google (OAuth): client id/secret shown, no SAML fields.
    expect(within(dialog).getByLabelText('Client ID')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Client secret')).toBeInTheDocument()
    expect(within(dialog).queryByLabelText('IdP SSO URL')).not.toBeInTheDocument()

    // Switch the type to SAML.
    await user.selectOptions(within(dialog).getByLabelText('Type'), 'saml')

    // IdP fields appear; client id/secret are hidden.
    expect(within(dialog).getByLabelText('IdP entity ID')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('IdP SSO URL')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('IdP signing certificate')).toBeInTheDocument()
    expect(within(dialog).queryByLabelText('Client ID')).not.toBeInTheDocument()
    expect(within(dialog).queryByLabelText('Client secret')).not.toBeInTheDocument()

    // SP registration details (ACS URL, SP entity id) derive live from the slug.
    expect(within(dialog).getByText(/ACS URL/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/SP entity ID/i)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Copy ACS URL' })).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Copy SP entity ID' })).toBeInTheDocument()
  })
})

describe('/settings?tab=users', () => {
  it('renders the users table with status chips and self-row protections', async () => {
    renderApp('/settings?tab=users')
    expect(await screen.findByText('ops@example.com')).toBeInTheDocument()
    expect(screen.getByText('newbie@example.com')).toBeInTheDocument()
    // Status chips: active admin, invited operator.
    expect(screen.getAllByText('Active').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Invited')).toBeInTheDocument()
    // Own row: role select disabled, no delete button.
    expect(screen.getByRole('combobox', { name: 'Role for ops@example.com' })).toBeDisabled()
    expect(
      screen.queryByRole('button', { name: 'Delete ops@example.com' }),
    ).not.toBeInTheDocument()
    // Other rows stay editable.
    expect(
      screen.getByRole('combobox', { name: 'Role for newbie@example.com' }),
    ).not.toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete newbie@example.com' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /invite user/i })).toBeInTheDocument()
  })

  it('renders the read-only SCIM directory-provisioning card', async () => {
    renderApp('/settings?tab=users')
    // The card renders its title and the SCIM base URL for the current origin.
    expect(await screen.findByText(/directory provisioning \(scim\)/i)).toBeInTheDocument()
    expect(screen.getByText(/\/scim\/v2$/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /copy scim base url/i })).toBeInTheDocument()
    // "API token" (exact — not the "API tokens" tab link) links across to the tokens tab.
    expect(screen.getByRole('link', { name: 'API token' })).toHaveAttribute(
      'href',
      expect.stringContaining('tab=tokens'),
    )
  })

  it('shows customer access: all-customers for admin/unscoped, chips for scoped users', async () => {
    renderApp('/settings?tab=users')
    // Scoped operator row is present.
    expect(await screen.findByText('scoped@example.com')).toBeInTheDocument()
    // Admin (ops) and the unscoped operator (newbie) both read "All customers".
    expect(screen.getAllByText('All customers').length).toBeGreaterThanOrEqual(2)
    // The scoped operator shows a chip with the customer name resolved from its id.
    expect(screen.getByText('ACME Corp')).toBeInTheDocument()
    // Non-admin rows expose an "Edit access" action; the admin row does not.
    expect(
      screen.getByRole('button', { name: 'Edit access for scoped@example.com' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Edit access for ops@example.com' }),
    ).not.toBeInTheDocument()
  })
})

describe('invite dialog customer access', () => {
  it('shows the customer multi-select only for non-admin roles', async () => {
    const user = userEvent.setup()
    renderApp('/settings?tab=users')
    await user.click(await screen.findByRole('button', { name: /invite user/i }))
    const dialog = await screen.findByRole('dialog')
    // Default role is operator → the customer access picker is shown.
    expect(within(dialog).getByText('Customer access')).toBeInTheDocument()
    expect(
      within(dialog).getByText('Leave empty for access to all customers.'),
    ).toBeInTheDocument()
    expect(within(dialog).getByRole('checkbox', { name: /ACME Corp/i })).toBeInTheDocument()
    // Switching to admin hides the picker and shows the muted note.
    await user.click(within(dialog).getByRole('radio', { name: /admin/i }))
    expect(
      within(dialog).queryByText('Leave empty for access to all customers.'),
    ).not.toBeInTheDocument()
    expect(within(dialog).getByText('Admins access all customers.')).toBeInTheDocument()
  })
})

describe('/settings?tab=tokens', () => {
  it('renders the tokens table with role/status badges and opens the create dialog', async () => {
    const user = userEvent.setup()
    renderApp('/settings?tab=tokens')
    // Rows: an active operator token and a revoked viewer token.
    expect(await screen.findByText('ci-deploy')).toBeInTheDocument()
    expect(screen.getByText('old-laptop')).toBeInTheDocument()
    expect(screen.getByText('otm_pat_7a3f')).toBeInTheDocument()
    // Status badges.
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText('Revoked')).toBeInTheDocument()
    // Active row has a Revoke action; the revoked row does not.
    expect(screen.getByRole('button', { name: 'Revoke' })).toBeInTheDocument()

    // Create dialog opens with the role radios.
    await user.click(screen.getByRole('button', { name: /create token/i }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByLabelText('Name')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /admin/i })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /operator/i })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /viewer/i })).toBeInTheDocument()
  })
})

describe('/settings?tab=alerts', () => {
  it('renders the channel table with per-type badges and a signed indicator only for webhooks', async () => {
    renderApp('/settings?tab=alerts')
    // Both channels render.
    expect(await screen.findByText('pagerduty-bridge')).toBeInTheDocument()
    expect(screen.getByText('ops-slack')).toBeInTheDocument()
    // Type badges: one Webhook, one Slack.
    expect(screen.getByText('Webhook')).toBeInTheDocument()
    expect(screen.getByText('Slack')).toBeInTheDocument()
    // The "signed" indicator only shows for the webhook-type row (hasSecret).
    expect(screen.getByText('signed')).toBeInTheDocument()
    // Actions stay available for both rows.
    expect(screen.getAllByRole('button', { name: 'Test' })).toHaveLength(2)
    expect(screen.getByRole('button', { name: 'Edit pagerduty-bridge' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete ops-slack' })).toBeInTheDocument()
  })

  it('hides the signing secret for Slack and shows it for Webhook in the create dialog', async () => {
    const user = userEvent.setup()
    renderApp('/settings?tab=alerts')
    await user.click(await screen.findByRole('button', { name: /add channel/i }))
    const dialog = await screen.findByRole('dialog')
    // Defaults to Webhook: the signing secret field is present.
    expect(within(dialog).getByLabelText(/signing secret/i)).toBeInTheDocument()
    expect(within(dialog).getByLabelText('URL')).toBeInTheDocument()
    // Switching to Slack hides the secret and relabels the URL field.
    await user.click(within(dialog).getByRole('radio', { name: /slack/i }))
    expect(within(dialog).queryByLabelText(/signing secret/i)).not.toBeInTheDocument()
    expect(within(dialog).getByLabelText('Slack incoming webhook URL')).toBeInTheDocument()
    // Switching back to Webhook brings the secret field back.
    await user.click(within(dialog).getByRole('radio', { name: /webhook/i }))
    expect(within(dialog).getByLabelText(/signing secret/i)).toBeInTheDocument()
  })
})

describe('/settings?tab=alert-rules', () => {
  it('renders the rules table with a human-readable condition, scope, and channel count', async () => {
    renderApp('/settings?tab=alert-rules')
    // Rule names.
    expect(await screen.findByText('ingest-drop')).toBeInTheDocument()
    expect(screen.getByText('error-spike')).toBeInTheDocument()
    // Human-readable conditions.
    expect(screen.getByText('Ingest items below 999999 over 5m')).toBeInTheDocument()
    expect(screen.getByText('Error logs above 50 over 5m')).toBeInTheDocument()
    // A PromQL rule shows its query as the condition (cluster-wide, no window).
    expect(screen.getByText('cpu-hot')).toBeInTheDocument()
    expect(screen.getByText('PromQL: avg(node_cpu_usage) above 0.8')).toBeInTheDocument()
    // Scope: null → All customers, else the customer name.
    expect(screen.getAllByText('All customers').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('ACME Corp')).toBeInTheDocument()
    // Channel counts (singular/plural).
    expect(screen.getByText('1 channel')).toBeInTheDocument()
    expect(screen.getAllByText('0 channels').length).toBeGreaterThanOrEqual(1)
    // Enabled toggles reflect each rule's state.
    expect(screen.getByRole('switch', { name: 'ingest-drop enabled' })).toBeChecked()
    expect(screen.getByRole('switch', { name: 'error-spike enabled' })).not.toBeChecked()
    // Per-row actions.
    expect(screen.getByRole('button', { name: 'Edit ingest-drop' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete error-spike' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /new rule/i })).toBeInTheDocument()
  })

  it('opens the create dialog with metric, comparison, window, scope, and channel controls', async () => {
    const user = userEvent.setup()
    renderApp('/settings?tab=alert-rules')
    await user.click(await screen.findByRole('button', { name: /new rule/i }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('Name')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Metric')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Comparison')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Threshold')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Window')).toBeInTheDocument()
    // Scope is editable on create and offers a customer option.
    const scope = within(dialog).getByLabelText('Customer scope')
    expect(scope).toBeEnabled()
    expect(within(scope).getByRole('option', { name: 'ACME Corp' })).toBeInTheDocument()
    // Channels come from the notification channels (webhooks) list.
    expect(within(dialog).getByText('pagerduty-bridge')).toBeInTheDocument()
  })

  it('reveals the PromQL query field and hides customer scope / window when the PromQL metric is chosen', async () => {
    const user = userEvent.setup()
    renderApp('/settings?tab=alert-rules')
    await user.click(await screen.findByRole('button', { name: /new rule/i }))
    const dialog = await screen.findByRole('dialog')
    // Default (ingest_items): scope + window shown, no query field.
    expect(within(dialog).getByLabelText('Customer scope')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Window')).toBeInTheDocument()
    expect(within(dialog).queryByLabelText('PromQL query')).not.toBeInTheDocument()

    // Switch to PromQL.
    await user.selectOptions(within(dialog).getByLabelText('Metric'), 'promql')

    // Query field appears; customer scope + window disappear; cluster-wide hint shows.
    expect(within(dialog).getByLabelText('PromQL query')).toBeInTheDocument()
    expect(within(dialog).queryByLabelText('Customer scope')).not.toBeInTheDocument()
    expect(within(dialog).queryByLabelText('Window')).not.toBeInTheDocument()
    expect(within(dialog).getByText('PromQL rules are cluster-wide.')).toBeInTheDocument()

    // Submitting with a name + threshold but an empty query is blocked with an inline error.
    await user.type(within(dialog).getByLabelText('Name'), 'cpu-hot')
    await user.type(within(dialog).getByLabelText('Threshold'), '0.8')
    await user.click(within(dialog).getByRole('button', { name: /create rule/i }))
    expect(within(dialog).getByText('Enter a PromQL query')).toBeInTheDocument()
  })
})

describe('/settings as non-admin', () => {
  it('shows the requires-admin page and hides admin nav items', async () => {
    stubApi({ me: testViewerMe })
    renderApp('/settings')
    expect(
      await screen.findByText('This page requires the admin role'),
    ).toBeInTheDocument()
    // Nav: admin-only entries hidden, general ones present.
    expect(screen.queryByRole('link', { name: /settings/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /audit/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /metrics/i })).toBeInTheDocument()
  })
})
