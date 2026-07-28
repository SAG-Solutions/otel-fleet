import { createFileRoute, redirect } from '@tanstack/react-router'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Check, ChevronsUpDown, ChartLine } from 'lucide-react'
import {
  getCustomerThroughputOptions,
  getMeOptions,
  listCustomersOptions,
} from '@/api/generated/@tanstack/react-query.gen'
import { isPortalUser } from '@/hooks/use-me'
import {
  DEFAULT_TIME_RANGE,
  EXTENDED_TIME_RANGES,
  isTimeRange,
  previousInterval,
  RANGE_LABEL,
  RANGE_STEP,
  rangeToInterval,
  type TimeRange,
} from '@/lib/time-range'
import { deltaPercent, seriesTotal, stepSeconds } from '@/features/metrics/metrics-math'
import { ExplorerChart, ExplorerChartSkeleton, type ExplorerSeries } from '@/features/metrics/explorer-chart'
import { CATEGORICAL_COLOR, SIGNAL_LABEL, SIGNALS } from '@/lib/chart-theme'
import { formatCompact } from '@/lib/format'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'
import { ErrorState } from '@/components/error-state'
import { TimeRangePicker } from '@/components/time-range-picker'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { Signal, ThroughputPoint } from '@/api/generated'

export const MAX_CUSTOMERS = 4

interface MetricsSearch {
  customers?: string[]
  signal?: Signal
  range?: TimeRange
  compare?: boolean
}

export const Route = createFileRoute('/_auth/metrics')({
  validateSearch: (search: Record<string, unknown>): MetricsSearch => ({
    customers: Array.isArray(search.customers)
      ? search.customers
          .filter((c): c is string => typeof c === 'string' && c !== '')
          .slice(0, MAX_CUSTOMERS)
      : undefined,
    signal: SIGNALS.includes(search.signal as Signal) ? (search.signal as Signal) : undefined,
    range: isTimeRange(search.range, EXTENDED_TIME_RANGES) ? search.range : undefined,
    compare: search.compare === true ? true : undefined,
  }),
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(getMeOptions())
    if (isPortalUser(me)) throw redirect({ to: '/' })
  },
  component: MetricsPage,
})

function MetricsPage() {
  const {
    customers = [],
    signal = 'logs',
    range = DEFAULT_TIME_RANGE,
    compare = false,
  } = Route.useSearch()
  const navigate = Route.useNavigate()

  const interval = rangeToInterval(range)
  const prevInterval = previousInterval(interval)
  const step = RANGE_STEP[range]

  const customersQuery = useQuery(listCustomersOptions())
  const nameById = new Map(
    (customersQuery.data?.customers ?? []).map((c) => [c.id, c.name] as const),
  )

  const currentQueries = useQueries({
    queries: customers.map((customerId) =>
      getCustomerThroughputOptions({
        path: { customerId },
        query: { from: interval.from, to: interval.to, step },
      }),
    ),
  })
  const previousQueries = useQueries({
    queries: compare
      ? customers.map((customerId) =>
          getCustomerThroughputOptions({
            path: { customerId },
            query: { from: prevInterval.from, to: prevInterval.to, step },
          }),
        )
      : [],
  })

  const setSearch = (patch: Partial<MetricsSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }), replace: true })

  const pending =
    currentQueries.some((q) => q.isPending) ||
    (compare && previousQueries.some((q) => q.isPending))
  const failed = currentQueries.find((q) => q.isError)

  const pickSignal = (points: { signal: Signal; points: ThroughputPoint[] }[] | undefined) =>
    points?.find((s) => s.signal === signal)?.points ?? []

  const series: ExplorerSeries[] = customers.map((customerId, index) => ({
    customerId,
    name: nameById.get(customerId) ?? customerId.slice(0, 8),
    colorIndex: index,
    points: pickSignal(currentQueries[index]?.data?.series),
    previousPoints: compare ? pickSignal(previousQueries[index]?.data?.series) : undefined,
  }))

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-lg font-semibold text-ink">Metrics explorer</h1>
        <p className="text-[13px] text-ink-2">
          Compare ingest throughput across customers, one signal at a time.
        </p>
      </div>

      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1.5">
          <Label id="metrics-customers-label">Customers (max {MAX_CUSTOMERS})</Label>
          <CustomerMultiSelect
            selected={customers}
            options={customersQuery.data?.customers ?? []}
            onChange={(next) =>
              setSearch({ customers: next.length === 0 ? undefined : next })
            }
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="metrics-signal">Signal</Label>
          <Select
            id="metrics-signal"
            className="w-32"
            value={signal}
            onChange={(e) => setSearch({ signal: e.target.value as Signal })}
          >
            {SIGNALS.map((s) => (
              <option key={s} value={s}>
                {SIGNAL_LABEL[s]}
              </option>
            ))}
          </Select>
        </div>
        <TimeRangePicker
          value={range}
          ranges={EXTENDED_TIME_RANGES}
          onChange={(next) => setSearch({ range: next })}
        />
        <label className="flex h-8 cursor-pointer items-center gap-2 text-xs text-ink-2 select-none">
          <Switch
            aria-label="Compare vs previous period"
            checked={compare}
            onCheckedChange={(checked) => setSearch({ compare: checked ? true : undefined })}
          />
          vs previous period
        </label>
      </div>

      {customers.length === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-14 text-center">
          <ChartLine className="size-5 text-ink-3" />
          <div className="text-sm font-semibold text-ink">Pick customers to compare</div>
          <p className="max-w-md text-[13px] text-ink-2">
            Select up to {MAX_CUSTOMERS} customers above to chart their {SIGNAL_LABEL[signal].toLowerCase()}{' '}
            throughput side by side, {RANGE_LABEL[range].toLowerCase()}.
          </p>
        </div>
      ) : failed !== undefined ? (
        <ErrorState
          title="Could not load throughput"
          onRetry={() => {
            for (const q of [...currentQueries, ...previousQueries]) {
              if (q.isError) void q.refetch()
            }
          }}
        />
      ) : pending ? (
        <div className="flex flex-col gap-4">
          <div className="rounded-lg border border-line bg-surface p-4">
            <ExplorerChartSkeleton />
          </div>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            {customers.map((id) => (
              <Skeleton key={id} className="h-16 w-full" />
            ))}
          </div>
        </div>
      ) : (
        <>
          <div className="rounded-lg border border-line bg-surface p-4">
            <ExplorerChart series={series} interval={interval} compare={compare} />
          </div>
          <SummaryStrip series={series} step={step} compare={compare} />
        </>
      )}
    </div>
  )
}

function CustomerMultiSelect({
  selected,
  options,
  onChange,
}: {
  selected: string[]
  options: { id: string; name: string }[]
  onChange: (next: string[]) => void
}) {
  const atLimit = selected.length >= MAX_CUSTOMERS
  const summary =
    selected.length === 0
      ? 'Select customers…'
      : options
          .filter((c) => selected.includes(c.id))
          .map((c) => c.name)
          .join(', ') || `${selected.length} selected`

  const toggle = (id: string) => {
    onChange(
      selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id],
    )
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-labelledby="metrics-customers-label"
        className="flex h-8 w-64 cursor-pointer items-center justify-between gap-2 rounded-md border border-line bg-transparent px-2.5 text-left text-[13px] text-ink transition-colors outline-none hover:bg-surface-2 focus-visible:border-accent/60 focus-visible:ring-2 focus-visible:ring-accent/30"
      >
        <span className={cn('min-w-0 truncate', selected.length === 0 && 'text-ink-3')}>
          {summary}
        </span>
        <ChevronsUpDown aria-hidden className="size-3.5 shrink-0 text-ink-3" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-72 w-64 overflow-y-auto">
        <DropdownMenuLabel>
          Customers{' '}
          <span className="font-normal text-ink-3">
            ({selected.length}/{MAX_CUSTOMERS})
          </span>
        </DropdownMenuLabel>
        {options.length === 0 && (
          <div className="px-2 py-1.5 text-xs text-ink-3">No customers yet.</div>
        )}
        {options.map((customer) => {
          const isSelected = selected.includes(customer.id)
          const disabled = !isSelected && atLimit
          return (
            <DropdownMenuItem
              key={customer.id}
              disabled={disabled}
              aria-checked={isSelected}
              role="menuitemcheckbox"
              onSelect={(e) => {
                e.preventDefault()
                toggle(customer.id)
              }}
            >
              <span className="inline-flex size-3.5 items-center justify-center">
                {isSelected && <Check className="size-3.5 text-accent" />}
              </span>
              <span className="min-w-0 truncate">{customer.name}</span>
            </DropdownMenuItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * Per-customer totals for the window with a muted delta vs the previous
 * period (dash when compare is off or the previous period was empty).
 */
function SummaryStrip({
  series,
  step,
  compare,
}: {
  series: ExplorerSeries[]
  step: string
  compare: boolean
}) {
  const { theme } = useTheme()
  const stepSec = stepSeconds(step)
  const palette = CATEGORICAL_COLOR[theme]

  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      {series.map((s) => {
        const total = seriesTotal(s.points, stepSec)
        const previousTotal =
          compare && s.previousPoints !== undefined
            ? seriesTotal(s.previousPoints, stepSec)
            : null
        const delta = previousTotal !== null ? deltaPercent(total, previousTotal) : null
        return (
          <div key={s.customerId} className="rounded-lg border border-line bg-surface px-4 py-3">
            <div className="flex items-center gap-1.5">
              <span
                aria-hidden
                className="h-2.5 w-0.75 rounded-[1px]"
                style={{ background: palette[s.colorIndex % palette.length] }}
              />
              <span className="truncate text-xs font-medium text-ink-2">{s.name}</span>
            </div>
            <div className="mt-1 flex items-baseline gap-2">
              <span className="font-mono text-lg font-semibold text-ink tabular-nums">
                {formatCompact(total)}
              </span>
              <span className="text-[11px] text-ink-3">items</span>
              <span className="ml-auto font-mono text-[11px] text-ink-2 tabular-nums">
                {!compare ? (
                  '—'
                ) : delta === null ? (
                  <Badge title="No volume in the previous period">new</Badge>
                ) : (
                  `${delta >= 0 ? '▲' : '▼'} ${Math.abs(delta).toFixed(Math.abs(delta) < 10 ? 1 : 0)}%`
                )}
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}
