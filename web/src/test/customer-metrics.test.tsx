import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderApp, stubApi, testCustomer } from '@/test/render-app'
import { setCsrfToken } from '@/lib/api-client'

// jsdom has no canvas — swap the ECharts binding for an inert ref.
vi.mock('@/hooks/use-echarts', () => ({
  useECharts: () => ({ current: null }),
}))

beforeEach(() => {
  stubApi()
})

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('/customers/$customerId (metrics tab)', () => {
  it('renders curated panels charting the scoped query_range result', async () => {
    renderApp(`/customers/${testCustomer.id}?tab=metrics`)

    // Curated panel titles + the scoped note render.
    expect(await screen.findByText('Auth requests by outcome')).toBeInTheDocument()
    expect(screen.getByText('Quota decisions')).toBeInTheDocument()
    expect(screen.getByText('Ad-hoc PromQL')).toBeInTheDocument()
    expect(
      screen.getByText(/queries only return this tenant's series/i),
    ).toBeInTheDocument()

    // Each curated panel draws the metric chart (2 curated panels with data).
    await waitFor(() =>
      expect(screen.getAllByRole('img', { name: 'Metric time series chart' })).toHaveLength(2),
    )
  })

  it('shows the empty state when a scoped query returns no series', async () => {
    stubApi({ metricSeries: [] })
    renderApp(`/customers/${testCustomer.id}?tab=metrics`)

    expect(await screen.findByText('Auth requests by outcome')).toBeInTheDocument()
    // Every curated panel falls back to the range-scoped empty state.
    await waitFor(() =>
      expect(screen.getAllByText('No data for this range').length).toBeGreaterThanOrEqual(2),
    )
  })
})
