import { useState } from 'react'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import { Download, Pencil } from 'lucide-react'
import {
  getBillingSettingsOptions,
  getBillingSettingsQueryKey,
  getBillingStatementOptions,
  getMeOptions,
  updateBillingSettingsMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import { isPortalUser } from '@/hooks/use-me'
import {
  formatBytes,
  formatCompact,
  formatMicro,
  formatRelative,
  microToUnit,
  unitToMicro,
} from '@/lib/format'
import { apiErrorMessage } from '@/lib/api-error'
import { AdminGate } from '@/components/admin-gate'
import { StatTile, StatTileSkeleton } from '@/components/stat-tile'
import { ErrorState } from '@/components/error-state'
import { toast } from '@/components/toaster'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type {
  BillingLine,
  BillingSettings,
  BillingStatement,
  GetBillingSettingsError,
} from '@/api/generated'

const MONTH_RE = /^\d{4}-\d{2}$/

/** Current calendar month as YYYY-MM (UTC). */
function currentMonth(): string {
  return new Date().toISOString().slice(0, 7)
}

interface BillingSearch {
  month?: string
}

export const Route = createFileRoute('/_auth/billing')({
  validateSearch: (search: Record<string, unknown>): BillingSearch => ({
    month: typeof search.month === 'string' && MONTH_RE.test(search.month) ? search.month : undefined,
  }),
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(getMeOptions())
    if (isPortalUser(me)) throw redirect({ to: '/' })
  },
  component: BillingPage,
})

function BillingPage() {
  const { month = currentMonth() } = Route.useSearch()
  const navigate = Route.useNavigate()

  const settings = useQuery(getBillingSettingsOptions())
  const statement = useQuery(getBillingStatementOptions({ query: { month } }))

  return (
    <AdminGate>
      <div className="flex flex-col gap-5">
        <div className="flex items-end justify-between gap-4">
          <div>
            <h1 className="text-lg font-semibold text-ink">Billing</h1>
            <p className="text-[13px] text-ink-2">Metered usage and pricing, per calendar month.</p>
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="billing-month">Month</Label>
            <input
              id="billing-month"
              type="month"
              value={month}
              max={currentMonth()}
              onChange={(e) => {
                const next = e.target.value
                void navigate({
                  search: { month: MONTH_RE.test(next) ? next : undefined },
                  replace: true,
                })
              }}
              className="h-8 rounded-md border border-line bg-transparent px-2.5 text-[13px] text-ink outline-none focus-visible:border-accent/60 focus-visible:ring-2 focus-visible:ring-accent/30"
            />
          </div>
        </div>

        <PricingCard query={settings} />

        {statement.isPending && <StatementSkeleton />}
        {statement.isError && (
          <ErrorState
            title="Could not load the billing statement"
            onRetry={() => void statement.refetch()}
          />
        )}
        {statement.isSuccess && <StatementBody statement={statement.data} />}
      </div>
    </AdminGate>
  )
}

function PricingCard({
  query,
}: {
  query: UseQueryResult<BillingSettings, GetBillingSettingsError>
}) {
  const [editing, setEditing] = useState(false)

  if (query.isPending) {
    return <Skeleton className="h-28 w-full" />
  }
  if (query.isError) {
    return <ErrorState title="Could not load pricing" onRetry={() => void query.refetch()} />
  }

  const settings = query.data
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">Pricing</h2>
          <p className="text-xs text-ink-2">
            Rates applied to metered usage. Updated {formatRelative(settings.updatedAt)}.
          </p>
        </div>
        {!editing && (
          <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
            <Pencil aria-hidden />
            Edit
          </Button>
        )}
      </div>
      {editing ? (
        <PricingForm settings={settings} onDone={() => setEditing(false)} />
      ) : (
        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <PriceStat
            label="Price per GiB"
            value={formatMicro(settings.pricePerGibMicro, settings.currency)}
          />
          <PriceStat
            label="Price per million items"
            value={formatMicro(settings.pricePerMillionItemsMicro, settings.currency)}
          />
          <PriceStat label="Currency" value={settings.currency} />
        </div>
      )}
    </div>
  )
}

function PriceStat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-ink-2">{label}</div>
      <div className="mt-0.5 font-mono text-sm text-ink">{value}</div>
    </div>
  )
}

function PricingForm({ settings, onDone }: { settings: BillingSettings; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [gib, setGib] = useState(String(microToUnit(settings.pricePerGibMicro)))
  const [items, setItems] = useState(String(microToUnit(settings.pricePerMillionItemsMicro)))
  const [currency, setCurrency] = useState(settings.currency)
  const [error, setError] = useState<string | null>(null)

  const update = useMutation({
    ...updateBillingSettingsMutation(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: getBillingSettingsQueryKey() })
      void queryClient.invalidateQueries({ queryKey: [{ _id: 'getBillingStatement' }] })
      toast('Pricing updated')
      onDone()
    },
    onError: (err) => setError(apiErrorMessage(err, 'Could not update pricing')),
  })

  const submit = () => {
    setError(null)
    const gibNum = Number(gib)
    const itemsNum = Number(items)
    if (!Number.isFinite(gibNum) || gibNum < 0 || !Number.isFinite(itemsNum) || itemsNum < 0) {
      setError('Enter valid, non-negative prices')
      return
    }
    if (!/^[A-Za-z]{3}$/.test(currency)) {
      setError('Currency must be a 3-letter code')
      return
    }
    update.mutate({
      body: {
        pricePerGibMicro: unitToMicro(gibNum),
        pricePerMillionItemsMicro: unitToMicro(itemsNum),
        currency: currency.toUpperCase(),
      },
    })
  }

  return (
    <div className="mt-3 flex flex-col gap-3">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="price-gib">Price per GiB</Label>
          <Input
            id="price-gib"
            type="number"
            min="0"
            step="0.01"
            value={gib}
            onChange={(e) => setGib(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="price-items">Price per million items</Label>
          <Input
            id="price-items"
            type="number"
            min="0"
            step="0.01"
            value={items}
            onChange={(e) => setItems(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="price-currency">Currency</Label>
          <Input
            id="price-currency"
            maxLength={3}
            value={currency}
            onChange={(e) => setCurrency(e.target.value)}
          />
        </div>
      </div>
      {error && <p className="text-[13px] text-danger">{error}</p>}
      <div className="flex items-center gap-2">
        <Button variant="primary" size="sm" onClick={submit} disabled={update.isPending}>
          {update.isPending ? 'Saving…' : 'Save pricing'}
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone} disabled={update.isPending}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function StatementBody({ statement }: { statement: BillingStatement }) {
  const { lines, currency, totalMicro } = statement
  const pricesUnset =
    statement.pricePerGibMicro === 0 && statement.pricePerMillionItemsMicro === 0

  if (lines.length === 0) {
    return (
      <div className="rounded-lg border border-line bg-surface p-10 text-center text-[13px] text-ink-2">
        No metered usage in {statement.month}.
      </div>
    )
  }

  return (
    <>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <StatTile label="Customers billed" value={formatCompact(lines.length)} />
        <StatTile label="Month" value={statement.month} />
        <StatTile label="Grand total" value={formatMicro(totalMicro, currency)} />
      </div>

      {pricesUnset && (
        <div className="rounded-lg border border-dashed border-line bg-surface px-4 py-3 text-[13px] text-ink-2">
          All prices are zero, so every total is 0.{' '}
          <Link to="/billing" className="text-accent hover:underline">
            Set pricing
          </Link>{' '}
          above to bill this usage.
        </div>
      )}

      <div className="flex justify-end">
        <Button variant="outline" size="sm" onClick={() => downloadCsv(statement)}>
          <Download aria-hidden />
          Export CSV
        </Button>
      </div>

      <div className="rounded-lg border border-line bg-surface">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Customer</TableHead>
              <TableHead className="text-right">Items</TableHead>
              <TableHead className="text-right">Volume</TableHead>
              <TableHead className="text-right">Volume cost</TableHead>
              <TableHead className="text-right">Items cost</TableHead>
              <TableHead className="text-right">Total</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {lines.map((line) => (
              <TableRow key={line.customerId}>
                <TableCell>
                  <Link
                    to="/customers/$customerId"
                    params={{ customerId: line.customerId }}
                    className="text-ink hover:text-accent"
                  >
                    {line.name}
                  </Link>
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] text-ink-2">
                  {formatCompact(line.items)}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] text-ink-2">
                  {formatBytes(line.bytes)}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] text-ink-2">
                  {formatMicro(line.bytesCostMicro, currency)}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] text-ink-2">
                  {formatMicro(line.itemsCostMicro, currency)}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] font-medium text-ink">
                  {formatMicro(line.totalMicro, currency)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
          <tfoot>
            <TableRow className="border-t border-line hover:bg-transparent">
              <TableCell className="font-medium text-ink">Total</TableCell>
              <TableCell />
              <TableCell />
              <TableCell />
              <TableCell />
              <TableCell className="text-right font-mono text-[13px] font-semibold text-ink">
                {formatMicro(totalMicro, currency)}
              </TableCell>
            </TableRow>
          </tfoot>
        </Table>
      </div>
    </>
  )
}

/** RFC-4180-ish cell: quote and double-embedded quotes when needed. */
function csvCell(value: string | number): string {
  const s = String(value)
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

function buildCsv(statement: BillingStatement): string {
  const header = ['Customer', 'Items', 'Bytes', 'Volume cost', 'Items cost', 'Total']
  const rows = statement.lines.map((line: BillingLine) => [
    line.name,
    line.items,
    line.bytes,
    microToUnit(line.bytesCostMicro).toFixed(2),
    microToUnit(line.itemsCostMicro).toFixed(2),
    microToUnit(line.totalMicro).toFixed(2),
  ])
  const total = ['Total', '', '', '', '', microToUnit(statement.totalMicro).toFixed(2)]
  return [header, ...rows, total].map((row) => row.map(csvCell).join(',')).join('\n')
}

function downloadCsv(statement: BillingStatement): void {
  const blob = new Blob([buildCsv(statement)], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `otelfleet-billing-${statement.month}.csv`
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

function StatementSkeleton() {
  return (
    <div className="flex flex-col gap-5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <StatTileSkeleton />
        <StatTileSkeleton />
        <StatTileSkeleton />
      </div>
      <Skeleton className="h-40 w-full" />
    </div>
  )
}
