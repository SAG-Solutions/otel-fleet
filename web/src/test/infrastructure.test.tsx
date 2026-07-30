import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderApp, stubApi, testViewerMe } from '@/test/render-app'
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

describe('/infrastructure as admin', () => {
  it('renders curated panels charting the query_range result', async () => {
    renderApp('/infrastructure')

    // Curated panel titles render, and each panel draws the metric chart.
    expect(await screen.findByText('Node CPU utilization')).toBeInTheDocument()
    expect(screen.getByText('Pods by namespace')).toBeInTheDocument()
    expect(screen.getByText('Ad-hoc PromQL')).toBeInTheDocument()

    // Panels with series show the chart — one per curated panel.
    await waitFor(() =>
      expect(screen.getAllByRole('img', { name: 'Metric time series chart' })).toHaveLength(5),
    )
  })

  it('shows the empty state when a query returns no series', async () => {
    stubApi({ metricSeries: [] })
    renderApp('/infrastructure')

    expect(await screen.findByText('Node CPU utilization')).toBeInTheDocument()
    // Every curated panel falls back to the No data empty state.
    await waitFor(() => expect(screen.getAllByText('No data')).toHaveLength(5))
    expect(
      screen.getAllByText(/enable the clusterMonitoring bundle or adjust the query/i).length,
    ).toBeGreaterThanOrEqual(1)
  })
})

describe('/infrastructure as non-admin', () => {
  it('shows the requires-admin page and no nav entry', async () => {
    stubApi({ me: testViewerMe })
    renderApp('/infrastructure')

    expect(await screen.findByText('This page requires the admin role')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /^infrastructure$/i })).not.toBeInTheDocument()
  })
})
