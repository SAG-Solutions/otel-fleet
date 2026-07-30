import { useState } from 'react'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Play } from 'lucide-react'
import { getMeOptions, queryMetricsRangeOptions } from '@/api/generated/@tanstack/react-query.gen'
import { isPortalUser } from '@/hooks/use-me'
import {
  isTimeRange,
  RANGE_LABEL,
  RANGE_STEP,
  rangeToInterval,
  TIME_RANGES,
  type Interval,
  type TimeRange,
} from '@/lib/time-range'
import {
  INFRA_PANELS,
  metricErrorDetail,
  type MetricPanelDef,
} from '@/features/infrastructure/metric-queries'
import { MetricChart, MetricChartSkeleton } from '@/features/infrastructure/metric-chart'
import { AdminGate } from '@/components/admin-gate'
import { ErrorState } from '@/components/error-state'
import { TimeRangePicker } from '@/components/time-range-picker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const INFRA_DEFAULT_RANGE: TimeRange = '6h'

interface InfraSearch {
  range?: TimeRange
}

export const Route = createFileRoute('/_auth/infrastructure')({
  validateSearch: (search: Record<string, unknown>): InfraSearch => ({
    range: isTimeRange(search.range, TIME_RANGES) ? search.range : undefined,
  }),
  beforeLoad: async ({ context }) => {
    // The proxy is admin-only; portal users never see this surface.
    const me = await context.queryClient.ensureQueryData(getMeOptions())
    if (isPortalUser(me)) throw redirect({ to: '/' })
  },
  component: InfrastructurePage,
})

function InfrastructurePage() {
  const { range = INFRA_DEFAULT_RANGE } = Route.useSearch()
  const navigate = Route.useNavigate()

  const interval = rangeToInterval(range)
  const step = RANGE_STEP[range]

  return (
    // Nav hides this for non-admins; AdminGate covers direct URL access with a
    // clean denied page (the endpoint 403s for non-admins anyway).
    <AdminGate>
      <div className="flex flex-col gap-5">
        <div className="flex items-end justify-between gap-4">
          <div>
            <h1 className="text-lg font-semibold text-ink">Infrastructure</h1>
            <p className="text-[13px] text-ink-2">
              Cluster metrics from VictoriaMetrics, {RANGE_LABEL[range].toLowerCase()}.
            </p>
          </div>
          <TimeRangePicker
            value={range}
            ranges={TIME_RANGES}
            onChange={(next) => void navigate({ search: { range: next }, replace: true })}
          />
        </div>

        <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
          {INFRA_PANELS.map((def) => (
            <MetricPanel key={def.id} def={def} interval={interval} step={step} />
          ))}
        </div>

        <AdHocPanel interval={interval} step={step} />
      </div>
    </AdminGate>
  )
}

/** Shared card chrome: a title, the PromQL as a mono caption, and a body. */
function PanelCard({
  title,
  query,
  children,
}: {
  title: string
  query: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
      <div>
        <h2 className="text-[13px] font-semibold text-ink">{title}</h2>
        <code className="block truncate font-mono text-[11px] text-ink-3" title={query}>
          {query}
        </code>
      </div>
      {children}
    </div>
  )
}

/** The empty state shown when a query returns no series. */
function NoDataState() {
  return (
    <div className="flex h-64 flex-col items-center justify-center gap-1 rounded-md border border-dashed border-line px-6 text-center">
      <div className="text-[13px] font-medium text-ink-2">No data</div>
      <p className="max-w-sm text-xs text-ink-3">
        Enable the clusterMonitoring bundle or adjust the query — the metric may not exist on this
        cluster.
      </p>
    </div>
  )
}

function MetricPanel({
  def,
  interval,
  step,
}: {
  def: MetricPanelDef
  interval: Interval
  step: string
}) {
  const query = useQuery(
    queryMetricsRangeOptions({
      query: { query: def.query, start: interval.from, end: interval.to, step },
    }),
  )

  return (
    <PanelCard title={def.title} query={def.query}>
      {query.isPending ? (
        <MetricChartSkeleton />
      ) : query.isError ? (
        <ErrorState
          title="Could not run query"
          detail={metricErrorDetail(query.error)}
          onRetry={() => void query.refetch()}
        />
      ) : query.data.series.length === 0 ? (
        <NoDataState />
      ) : (
        <MetricChart series={query.data.series} interval={interval} />
      )}
    </PanelCard>
  )
}

/**
 * Free-form PromQL against the same endpoint — makes the view useful even
 * when the curated metric names don't match a given cluster. 400s surface the
 * validation message inline; 503s report the store as unavailable.
 */
function AdHocPanel({ interval, step }: { interval: Interval; step: string }) {
  const [input, setInput] = useState('')
  const [submitted, setSubmitted] = useState('')

  const query = useQuery({
    ...queryMetricsRangeOptions({
      query: { query: submitted, start: interval.from, end: interval.to, step },
    }),
    enabled: submitted !== '',
  })

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-line bg-surface p-4">
      <div>
        <h2 className="text-[13px] font-semibold text-ink">Ad-hoc PromQL</h2>
        <p className="text-xs text-ink-2">
          Run any PromQL expression against VictoriaMetrics and chart the result.
        </p>
      </div>

      <form
        className="flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setSubmitted(input.trim())
        }}
      >
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="infra-promql">Expression</Label>
          <Input
            id="infra-promql"
            className="font-mono"
            placeholder="sum by (k8s_node_name) (k8s_node_cpu_utilization)"
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
        </div>
        <Button type="submit" variant="primary" size="sm" disabled={input.trim() === ''}>
          <Play aria-hidden />
          Run
        </Button>
      </form>

      {submitted === '' ? (
        <div className="flex h-64 items-center justify-center rounded-md border border-dashed border-line px-6 text-center text-[13px] text-ink-3">
          Enter a PromQL expression and run it to chart the result.
        </div>
      ) : query.isPending ? (
        <MetricChartSkeleton />
      ) : query.isError ? (
        <ErrorState
          title="Query failed"
          detail={metricErrorDetail(query.error)}
          onRetry={() => void query.refetch()}
        />
      ) : query.data.series.length === 0 ? (
        <NoDataState />
      ) : (
        <MetricChart series={query.data.series} interval={interval} />
      )}
    </div>
  )
}
