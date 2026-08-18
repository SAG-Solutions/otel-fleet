import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import { renderApp, stubApi, testViewerMe } from '@/test/render-app'
import { setCsrfToken } from '@/lib/api-client'

beforeEach(() => {
  stubApi()
})

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('/billing as admin', () => {
  it('renders the pricing card and the statement table with formatted currency totals', async () => {
    renderApp('/billing?month=2026-07')

    // Pricing card shows rates as decimals with the currency code. Scope to
    // the card since "0.50 EUR" also appears as a statement cost cell.
    const gibLabel = await screen.findByText('Price per GiB')
    const card = gibLabel.closest('div.rounded-lg') as HTMLElement
    expect(within(card).getByText('0.50 EUR')).toBeInTheDocument()
    expect(within(card).getByText('2.00 EUR')).toBeInTheDocument()

    // Statement table has a customer row with a formatted currency total.
    const acmeCell = screen.getByRole('link', { name: 'ACME Corp' })
    const row = acmeCell.closest('tr')
    expect(row).not.toBeNull()
    expect(within(row as HTMLElement).getByText('7.00 EUR')).toBeInTheDocument()

    // Grand total appears (StatTile + footer row).
    expect(screen.getAllByText('10.50 EUR').length).toBeGreaterThanOrEqual(1)

    // Export button is available.
    expect(screen.getByRole('button', { name: /export csv/i })).toBeInTheDocument()
  })

  it('lists per-customer price overrides and flags overridden statement rows', async () => {
    renderApp('/billing?month=2026-07')

    // The overrides card lists the fixture override (Globex): custom GiB rate,
    // inherited (global) items rate.
    const heading = await screen.findByText('Per-customer overrides')
    const card = heading.closest('div.rounded-lg') as HTMLElement
    const overrideRow = (await within(card).findByText('Globex Inc')).closest('tr') as HTMLElement
    expect(within(overrideRow).getByText('1.00 EUR')).toBeInTheDocument()
    expect(within(overrideRow).getByText('global')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Add override' })).toBeInTheDocument()

    // The overridden customer's statement row carries an "override" badge.
    const statementRow = screen.getByRole('link', { name: 'Globex Inc' }).closest('tr') as HTMLElement
    expect(within(statementRow).getByText('override')).toBeInTheDocument()
    // ACME (no override) does not.
    const acmeRow = screen.getByRole('link', { name: 'ACME Corp' }).closest('tr') as HTMLElement
    expect(within(acmeRow).queryByText('override')).not.toBeInTheDocument()
  })
})

describe('/billing as non-admin', () => {
  it('shows the requires-admin page', async () => {
    stubApi({ me: testViewerMe })
    renderApp('/billing')
    expect(await screen.findByText('This page requires the admin role')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /^billing$/i })).not.toBeInTheDocument()
  })
})
