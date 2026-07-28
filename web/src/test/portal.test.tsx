import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import { renderApp, stubApi, testCustomer, testMe, testPortalMe } from '@/test/render-app'
import { setCsrfToken } from '@/lib/api-client'

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('tenant self-service portal', () => {
  it('shows the portal nav and tenant overview for a scoped user', async () => {
    stubApi({ me: testPortalMe, customers: [testCustomer] })
    renderApp('/')

    const nav = await screen.findByRole('navigation', { name: 'Main' })
    // Portal nav entries.
    expect(within(nav).getByRole('link', { name: /overview/i })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: /pipelines/i })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: /agents/i })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: /explore/i })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: /usage & cost/i })).toBeInTheDocument()
    // Admin/fleet-wide surfaces are absent.
    for (const label of [/customers/i, /settings/i, /billing/i, /audit/i, /metrics/i]) {
      expect(within(nav).queryByRole('link', { name: label })).not.toBeInTheDocument()
    }

    // Overview renders the scoped customer (its name + client id).
    expect(await screen.findAllByText(testCustomer.name)).not.toHaveLength(0)
    expect(screen.getByText(testCustomer.clientId)).toBeInTheDocument()
  })

  it('shows the full admin nav for an admin', async () => {
    stubApi({ me: testMe })
    renderApp('/')

    const nav = await screen.findByRole('navigation', { name: 'Main' })
    expect(within(nav).getByRole('link', { name: /customers/i })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: /settings/i })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: /audit/i })).toBeInTheDocument()
  })

  it('redirects a portal user away from admin-only routes', async () => {
    stubApi({ me: testPortalMe, customers: [testCustomer] })
    renderApp('/settings')

    // The beforeLoad guard bounces them to the portal overview; the admin
    // Settings surface never renders.
    await screen.findByRole('navigation', { name: 'Main' })
    expect(screen.queryByText('This page requires the admin role')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /settings/i })).not.toBeInTheDocument()
  })
})
