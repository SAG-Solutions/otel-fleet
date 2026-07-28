import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { getStatsOverviewOptions } from '@/api/generated/@tanstack/react-query.gen'
import {
  DEFAULT_TIME_RANGE,
  isTimeRange,
  RANGE_LABEL,
  rangeSeconds,
  rangeToInterval,
  type TimeRange,
} from '@/lib/time-range'
import { formatCompact, formatNumber, formatRate } from '@/lib/format'
import { SIGNAL_COLOR, SIGNAL_LABEL, SIGNALS } from '@/lib/chart-theme'
import { useTheme } from '@/lib/theme'
import { TimeRangePicker } from '@/components/time-range-picker'
import { StatTile, StatTileSkeleton } from '@/components/stat-tile'
import { TerminalHint, TELEMETRYGEN_COMMAND } from '@/components/terminal-hint'
import { ErrorState } from '@/components/error-state'
import { useMe, isPortalUser } from '@/hooks/use-me'
import { PortalOverview } from '@/features/portal/portal-overview'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { StatsOverview } from '@/api/generated'

interface DashboardSearch {
  range?: TimeRange
}

export const Route = createFileRoute('/_auth/')({
  validateSearch: (search: Record<string, unknown>): DashboardSearch => ({
    range: isTimeRange(search.range) ? search.range : undefined,
  }),
  component: DashboardPage,
})

function DashboardPage() {
  const me = useMe()
  if (isPortalUser(me)) return <PortalOverview />
  return <FleetDashboard />
}

function FleetDashboard() {
  const { range = DEFAULT_TIME_RANGE } = Route.useSearch()
  const navigate = Route.useNavigate()
  const interval = rangeToInterval(range)

  const overviewQuery = useQuery(
    getStatsOverviewOptions({ query: { from: interval.from, to: interval.to } }),
  )

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold text-ink">Dashboard</h1>
          <p className="text-[13px] text-ink-2">
            Fleet-wide ingest, {RANGE_LABEL[range].toLowerCase()}
          </p>
        </div>
        <TimeRangePicker
          value={range}
          onChange={(next) => void navigate({ search: { range: next }, replace: true })}
        />
      </div>

      {overviewQuery.isPending && <DashboardSkeleton />}
      {overviewQuery.isError && (
        <ErrorState
          title="Could not load fleet stats"
          onRetry={() => void overviewQuery.refetch()}
        />
      )}
      {overviewQuery.isSuccess && <DashboardBody overview={overviewQuery.data} range={range} />}
    </div>
  )
}

function DashboardBody({ overview, range }: { overview: StatsOverview; range: TimeRange }) {
  const { theme } = useTheme()
  const seconds = rangeSeconds(range)
  const totalItems = overview.totals.logs + overview.totals.traces + overview.totals.metrics
  const refused = overview.refusedRequests ?? 0

  return (
    <>
      <div className="grid grid-cols-2 gap-3 min-[1200px]:grid-cols-5 lg:grid-cols-3">
        <StatTile label="Active customers" value={formatNumber(overview.activeCustomers)} />
        {SIGNALS.map((signal) => (
          <StatTile
            key={signal}
            label={SIGNAL_LABEL[signal]}
            markColor={SIGNAL_COLOR[signal][theme]}
            value={formatRate(overview.totals[signal] / seconds)}
            unit="items/s"
            hint={`${formatCompact(overview.totals[signal])} total`}
          />
        ))}
        <StatTile
          label="Refused requests"
          value={formatCompact(refused)}
          tone={refused > 0 ? 'danger' : 'default'}
          hint={refused > 0 ? 'Auth-refused ingest in range' : 'No auth failures in range'}
        />
      </div>

      {totalItems === 0 && overview.topCustomers.length === 0 ? (
        <TerminalHint
          title="No telemetry in this range"
          body="Nothing has been ingested yet. Point an exporter at the gateway with a customer API key — telemetrygen is the quickest smoke test."
          command={TELEMETRYGEN_COMMAND}
        />
      ) : (
        <TopCustomersTable overview={overview} />
      )}
    </>
  )
}

function TopCustomersTable({ overview }: { overview: StatsOverview }) {
  const max = Math.max(...overview.topCustomers.map((c) => c.items), 1)
  return (
    <section className="rounded-lg border border-line bg-surface">
      <div className="flex items-center justify-between border-b border-line px-4 py-3">
        <h2 className="text-[13px] font-semibold text-ink">Top customers by volume</h2>
        <Link
          to="/customers"
          className="rounded text-xs text-accent outline-none hover:underline focus-visible:ring-2 focus-visible:ring-accent/70"
        >
          All customers
        </Link>
      </div>
      {overview.topCustomers.length === 0 ? (
        <p className="px-4 py-6 text-[13px] text-ink-2">No per-customer volume in this range.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="w-8">#</TableHead>
              <TableHead>Customer</TableHead>
              <TableHead className="text-right">Items</TableHead>
              <TableHead className="w-1/3">Share</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {overview.topCustomers.map((customer, index) => (
              <TableRow key={customer.customerId}>
                <TableCell className="font-mono text-xs text-ink-3">{index + 1}</TableCell>
                <TableCell>
                  <Link
                    to="/customers/$customerId"
                    params={{ customerId: customer.customerId }}
                    className="rounded font-medium text-ink outline-none hover:text-accent hover:underline focus-visible:ring-2 focus-visible:ring-accent/70"
                  >
                    {customer.name}
                  </Link>
                </TableCell>
                <TableCell className="text-right font-mono text-xs tabular-nums">
                  {formatCompact(customer.items)}
                </TableCell>
                <TableCell>
                  <div
                    className="h-1.5 rounded-full bg-accent/70"
                    role="presentation"
                    style={{ width: `${Math.max((customer.items / max) * 100, 2)}%` }}
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

function DashboardSkeleton() {
  return (
    <>
      <div className="grid grid-cols-2 gap-3 min-[1200px]:grid-cols-5 lg:grid-cols-3">
        {Array.from({ length: 5 }, (_, i) => (
          <StatTileSkeleton key={i} />
        ))}
      </div>
      <div className="rounded-lg border border-line bg-surface p-4">
        <Skeleton className="h-4 w-44" />
        <Skeleton className="mt-3 h-32 w-full" />
      </div>
    </>
  )
}
