import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ExporterGallery } from '@/features/pipelines/exporter-gallery'
import { useDraftStore } from '@/features/pipelines/draft-store'
import type { CatalogComponent, CatalogPreset } from '@/api/generated'

const presets: CatalogPreset[] = [
  {
    id: 'loki',
    displayName: 'Grafana Loki',
    description: 'Ship logs to a Loki backend.',
    icon: 'loki',
    exporterType: 'otlphttp',
    defaults: { endpoint: 'https://loki.example.com/otlp' },
  },
]

const exporters: CatalogComponent[] = [
  {
    type: 'debug',
    kind: 'exporter',
    displayName: 'Debug',
    description: 'Write telemetry to the collector log.',
    schema: { type: 'object', properties: {} },
    defaults: {},
  },
]

beforeEach(() => {
  // Seed a draft graph with a single existing exporter so index math is real.
  useDraftStore.getState().seed(
    'test-pipeline',
    {
      signals: ['logs'],
      processors: [],
      exporters: [{ type: 'debug', config: {} }],
    },
    1,
  )
})

describe('ExporterGallery', () => {
  it('shows preset cards and adds an exporter node from a preset with its defaults', async () => {
    const user = userEvent.setup()
    render(<ExporterGallery presets={presets} exporters={exporters} />)

    await user.click(screen.getByRole('button', { name: /add exporter/i }))

    // Preset card is visible in the gallery.
    expect(await screen.findByText('Grafana Loki')).toBeInTheDocument()
    expect(screen.getByText('Ship logs to a Loki backend.')).toBeInTheDocument()

    await user.click(screen.getByText('Grafana Loki'))

    const { exporters: added } = useDraftStore.getState().graph
    expect(added).toHaveLength(2)
    // Preset instantiates its underlying exporter type, config from defaults.
    expect(added[1]).toEqual({
      type: 'otlphttp',
      config: { endpoint: 'https://loki.example.com/otlp' },
    })
  })

  it('adds a raw exporter from the "All exporters" tab', async () => {
    const user = userEvent.setup()
    render(<ExporterGallery presets={presets} exporters={exporters} />)

    await user.click(screen.getByRole('button', { name: /add exporter/i }))
    await user.click(await screen.findByRole('tab', { name: /all exporters/i }))
    await user.click(screen.getByText('Debug'))

    const { exporters: added } = useDraftStore.getState().graph
    expect(added).toHaveLength(2)
    expect(added[1]).toEqual({ type: 'debug', config: {} })
  })
})
