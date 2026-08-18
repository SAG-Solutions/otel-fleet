import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { renderApp, stubApi, testCustomer } from '@/test/render-app'
import { setCsrfToken } from '@/lib/api-client'

beforeEach(() => {
  stubApi()
})

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('/login', () => {
  it('renders SSO providers and the dev login form', async () => {
    renderApp('/login')
    expect(await screen.findByText('Continue with Google')).toBeInTheDocument()
    expect(screen.getByLabelText('Email')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /dev login/i })).toBeInTheDocument()
    expect(screen.getAllByText(/otel/i).length).toBeGreaterThan(0)
  })
})

describe('/ (dashboard)', () => {
  it('renders stat tiles and the top-customers table behind the auth guard', async () => {
    renderApp('/')
    expect(await screen.findByText('Active customers')).toBeInTheDocument()
    expect(screen.getByText('Refused requests')).toBeInTheDocument()
    expect(screen.getByText('Top customers by volume')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'ACME Corp' })).toBeInTheDocument()
    // Time-range picker present with 24h default selected.
    expect(screen.getByRole('radio', { name: '24h' })).toHaveAttribute('aria-checked', 'true')
  })
})

describe('/customers', () => {
  it('renders the customer grid with tenant ID and status', async () => {
    renderApp('/customers')
    expect(await screen.findByRole('link', { name: 'ACME Corp' })).toBeInTheDocument()
    expect(screen.getByText('cust_7f3a9b2c')).toBeInTheDocument()
    expect(screen.getAllByText('Active').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByRole('button', { name: /new customer/i })).toBeInTheDocument()
  })
})

describe('/customers/$customerId (api-keys tab)', () => {
  it('renders the key table with prefix and revoke action for operators', async () => {
    renderApp(`/customers/${testCustomer.id}?tab=api-keys`)
    expect(await screen.findByText('prod-gateway')).toBeInTheDocument()
    expect(screen.getByText('otm_ab12cd34')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /revoke/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create key/i })).toBeInTheDocument()
  })
})
