import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Play } from 'lucide-react'
import { queryCustomerMetricsRangeOptions } from '@/api/generated/@tanstack/react-query.gen'
import {
  DEFAULT_TIME_RANGE,
  RANGE_STEP,
  rangeToInterval,
  type Interval,
  type TimeRange,
} from '@/lib/time-range'
import { metricErrorDetail, type MetricPanelDef } from '@/features/infrastructure/metric-queries'
import { MetricChart, MetricChartSkeleton } from '@/features/infrastructure/metric-chart'
import { ErrorState } from '@/components/error-state'
import { TimeRangePicker } from '@/components/time-range-picker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/**
 * Curated per-customer panels. Every selector the server sees is scoped to
 * this tenant (VictoriaMetrics extra_filters tenant_id), so these charts only
 * ever draw THIS customer's series. Metric names follow OTel semconv
 * (underscored) and the data varies, so each panel shows a graceful empty
 * state when its series come back empty.
 */
const CUSTOMER_PANELS: readonly MetricPanelDef[] = [
  {
    id: 'auth-requests',
    title: 'Auth requests by outcome',
    query: 'sum by (outcome) (rate(otel_fleet_auth_requests_total[5m]))',
  },
  {
    id: 'quota-decisions',
    title: 'Quota decisions',
    query: 'sum by (decision) (rate(otel_fleet_quota_decisions_total[5m]))',
  },
] as const

export function CustomerMetrics({ customerId }: { customerId: string }) {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE)
  const interval = rangeToInterval(range)
  const step = RANGE_STEP[range]

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">Metrics</h2>
          <p className="text-xs text-ink-2">
            Scoped to this customer — queries only return this tenant&apos;s series.
          </p>
        </div>
        <TimeRangePicker value={range} onChange={setRange} />
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        {CUSTOMER_PANELS.map((def) => (
          <MetricPanel key={def.id} customerId={customerId} def={def} interval={interval} step={step} />
        ))}
      </div>

      <AdHocPanel customerId={customerId} interval={interval} step={step} />
    </div>
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
        <h3 className="text-[13px] font-semibold text-ink">{title}</h3>
        <code className="block truncate font-mono text-[11px] text-ink-3" title={query}>
          {query}
        </code>
      </div>
      {children}
    </div>
  )
}

/** The empty state shown when a scoped query returns no series. */
function NoDataState() {
  return (
    <div className="flex h-64 flex-col items-center justify-center gap-1 rounded-md border border-dashed border-line px-6 text-center">
      <div className="text-[13px] font-medium text-ink-2">No data for this range</div>
      <p className="max-w-sm text-xs text-ink-3">
        This customer emitted nothing matching the query in the selected window, or the metric does
        not exist yet.
      </p>
    </div>
  )
}

function MetricPanel({
  customerId,
  def,
  interval,
  step,
}: {
  customerId: string
  def: MetricPanelDef
  interval: Interval
  step: string
}) {
  const query = useQuery(
    queryCustomerMetricsRangeOptions({
      path: { customerId },
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
 * Free-form PromQL against the scoped endpoint — every selector is still
 * pinned to this tenant server-side, so the user can only ever see their own
 * series. 400s surface the validation message inline; 503s report the store
 * as unavailable; 404 (customer) surfaces its message.
 */
function AdHocPanel({
  customerId,
  interval,
  step,
}: {
  customerId: string
  interval: Interval
  step: string
}) {
  const [input, setInput] = useState('')
  const [submitted, setSubmitted] = useState('')

  const query = useQuery({
    ...queryCustomerMetricsRangeOptions({
      path: { customerId },
      query: { query: submitted, start: interval.from, end: interval.to, step },
    }),
    enabled: submitted !== '',
  })

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-line bg-surface p-4">
      <div>
        <h3 className="text-[13px] font-semibold text-ink">Ad-hoc PromQL</h3>
        <p className="text-xs text-ink-2">
          Run any PromQL expression — it is scoped to this customer before it reaches the store.
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
          <Label htmlFor="customer-promql">Expression</Label>
          <Input
            id="customer-promql"
            className="font-mono"
            placeholder="sum by (outcome) (rate(otel_fleet_auth_requests_total[5m]))"
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
