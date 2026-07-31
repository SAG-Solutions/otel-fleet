import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, KeyRound, Pause, Play, Plus } from 'lucide-react'
import {
  getCustomerOptions,
  getCustomerQueryKey,
  getCustomerThroughputOptions,
  listApiKeysOptions,
  listApiKeysQueryKey,
  listCustomersQueryKey,
  revokeApiKeyMutation,
  updateCustomerMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import {
  DEFAULT_TIME_RANGE,
  isTimeRange,
  RANGE_STEP,
  rangeToInterval,
  type TimeRange,
} from '@/lib/time-range'
import { formatDate, formatDateTime, formatRelative } from '@/lib/format'
import { SIGNALS } from '@/lib/chart-theme'
import { useMe, canMutate } from '@/hooks/use-me'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { toast } from '@/components/toaster'
import { apiErrorMessage } from '@/lib/api-error'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { ErrorState } from '@/components/error-state'
import { TimeRangePicker } from '@/components/time-range-picker'
import { TerminalHint, TELEMETRYGEN_COMMAND } from '@/components/terminal-hint'
import { ThroughputChartCard, ThroughputChartSkeleton } from '@/components/throughput-chart'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { CreateApiKeyDialog } from '@/components/create-api-key-dialog'
import { SecretDialog } from '@/components/secret-dialog'
import { CustomerAgentsTab } from '@/features/fleet/customer-agents-tab'
import { CustomerMetrics } from '@/features/customers/customer-metrics'
import type { ApiKey, ApiKeyCreated, Customer, ThroughputPoint } from '@/api/generated'

const TABS = ['overview', 'metrics', 'api-keys', 'agents', 'settings'] as const
type Tab = (typeof TABS)[number]

interface CustomerSearch {
  tab?: Tab
  range?: TimeRange
}

export const Route = createFileRoute('/_auth/customers/$customerId')({
  validateSearch: (search: Record<string, unknown>): CustomerSearch => ({
    tab: TABS.includes(search.tab as Tab) ? (search.tab as Tab) : undefined,
    range: isTimeRange(search.range) ? search.range : undefined,
  }),
  component: CustomerDetailPage,
})

function CustomerDetailPage() {
  const { customerId } = Route.useParams()
  const { tab = 'overview' } = Route.useSearch()

  const customerQuery = useQuery(getCustomerOptions({ path: { customerId } }))

  if (customerQuery.isPending) return <CustomerDetailSkeleton />
  if (customerQuery.isError) {
    return (
      <ErrorState
        title="Could not load this customer"
        detail="It may have been deleted, or the request failed."
        onRetry={() => void customerQuery.refetch()}
      />
    )
  }

  const customer = customerQuery.data

  return (
    <div className="flex flex-col gap-5">
      <CustomerHeader customer={customer} />
      <TabBar customerId={customerId} active={tab} />
      {tab === 'overview' && <OverviewTab customerId={customerId} />}
      {tab === 'metrics' && <CustomerMetrics customerId={customerId} />}
      {tab === 'api-keys' && <ApiKeysTab customerId={customerId} />}
      {tab === 'agents' && <CustomerAgentsTab customerId={customerId} />}
      {tab === 'settings' && <CustomerSettingsTab customer={customer} />}
    </div>
  )
}

function CustomerHeader({ customer }: { customer: Customer }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const [confirmSuspend, setConfirmSuspend] = useState(false)

  const update = useMutation({
    ...updateCustomerMutation(),
    onSuccess: (updated) => {
      queryClient.setQueryData(getCustomerQueryKey({ path: { customerId: customer.id } }), updated)
      void queryClient.invalidateQueries({ queryKey: listCustomersQueryKey() })
      setConfirmSuspend(false)
    },
  })

  const setStatus = (status: 'active' | 'suspended') =>
    update.mutate({ path: { customerId: customer.id }, body: { status } })

  return (
    <div className="flex flex-col gap-3">
      <Link
        to="/customers"
        className="inline-flex w-fit items-center gap-1 rounded text-xs text-ink-3 outline-none hover:text-ink focus-visible:ring-2 focus-visible:ring-accent/70"
      >
        <ArrowLeft className="size-3" />
        Customers
      </Link>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <h1 className="text-lg font-semibold text-ink">{customer.name}</h1>
          <StatusBadge status={customer.status} />
        </div>
        {canMutate(me) && customer.status !== 'deleted' && (
          <>
            {customer.status === 'active' ? (
              <Button
                variant="outline"
                onClick={() => setConfirmSuspend(true)}
                disabled={update.isPending}
              >
                <Pause aria-hidden />
                Suspend
              </Button>
            ) : (
              <Button
                variant="primary"
                onClick={() => setStatus('active')}
                disabled={update.isPending}
              >
                <Play aria-hidden />
                {update.isPending ? 'Activating…' : 'Activate'}
              </Button>
            )}
          </>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-ink-2">
        <span className="inline-flex items-center gap-1">
          <span className="text-ink-3">client ID</span>
          <code className="font-mono">{customer.clientId}</code>
          <CopyButton value={customer.clientId} label="Copy client ID" />
        </span>
        <span>
          <span className="text-ink-3">slug</span>{' '}
          <code className="font-mono">{customer.slug}</code>
        </span>
        <span>
          <span className="text-ink-3">since</span> {formatDate(customer.createdAt)}
        </span>
      </div>
      {update.isError && (
        <p role="alert" className="text-xs text-danger">
          Status change failed. Retry, or check your role.
        </p>
      )}
      <ConfirmDialog
        open={confirmSuspend}
        onOpenChange={setConfirmSuspend}
        title={`Suspend ${customer.name}?`}
        description="Ingest for this customer is refused at the gateway while suspended. Existing data is kept and you can re-activate at any time."
        confirmLabel="Suspend customer"
        destructive
        pending={update.isPending}
        onConfirm={() => setStatus('suspended')}
      />
    </div>
  )
}

function TabBar({ customerId, active }: { customerId: string; active: Tab }) {
  const labels: Record<Tab, string> = {
    overview: 'Overview',
    metrics: 'Metrics',
    'api-keys': 'API keys',
    agents: 'Agents',
    settings: 'Settings',
  }
  return (
    <nav aria-label="Customer sections" className="flex gap-1 border-b border-line">
      {TABS.map((tab) => (
        <Link
          key={tab}
          to="/customers/$customerId"
          params={{ customerId }}
          search={(prev) => ({ ...prev, tab })}
          aria-current={active === tab ? 'page' : undefined}
          className={cn(
            '-mb-px rounded-t px-3 py-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-accent/70',
            active === tab
              ? 'border-b-2 border-accent font-medium text-ink'
              : 'border-b-2 border-transparent text-ink-2 hover:text-ink',
          )}
        >
          {labels[tab]}
        </Link>
      ))}
    </nav>
  )
}

function OverviewTab({ customerId }: { customerId: string }) {
  const { range = DEFAULT_TIME_RANGE } = Route.useSearch()
  const navigate = Route.useNavigate()
  const interval = rangeToInterval(range)

  const throughputQuery = useQuery(
    getCustomerThroughputOptions({
      path: { customerId },
      query: { from: interval.from, to: interval.to, step: RANGE_STEP[range] },
    }),
  )

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-[13px] font-semibold text-ink">Ingest throughput</h2>
        <TimeRangePicker
          value={range}
          onChange={(next) =>
            void navigate({ search: (prev) => ({ ...prev, range: next }), replace: true })
          }
        />
      </div>

      {throughputQuery.isPending && (
        <div className="grid gap-4">
          {SIGNALS.map((signal) => (
            <ThroughputChartSkeleton key={signal} />
          ))}
        </div>
      )}
      {throughputQuery.isError && (
        <ErrorState
          title="Could not load throughput"
          onRetry={() => void throughputQuery.refetch()}
        />
      )}
      {throughputQuery.isSuccess && <ThroughputCharts series={throughputQuery.data.series} />}
    </div>
  )
}

function ThroughputCharts({
  series,
}: {
  series: { signal: 'logs' | 'traces' | 'metrics'; points: ThroughputPoint[] }[]
}) {
  const bySignal = new Map(series.map((s) => [s.signal, s.points]))
  const hasData = series.some((s) => s.points.some((p) => p.value > 0))

  if (!hasData) {
    return (
      <TerminalHint
        title="No throughput in this range"
        body="This customer has not ingested anything in the selected window. Send a smoke signal with one of its API keys:"
        command={TELEMETRYGEN_COMMAND}
      />
    )
  }

  return (
    <div className="grid gap-4">
      {SIGNALS.map((signal) => {
        const points = bySignal.get(signal) ?? []
        const last = points.at(-1)
        return (
          <ThroughputChartCard
            key={signal}
            signal={signal}
            points={points}
            currentRate={last ? last.value : null}
          />
        )
      })}
    </div>
  )
}

function ApiKeysTab({ customerId }: { customerId: string }) {
  const me = useMe()
  const [createOpen, setCreateOpen] = useState(false)
  const [createdKey, setCreatedKey] = useState<ApiKeyCreated | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null)
  const queryClient = useQueryClient()

  const keysQuery = useQuery(listApiKeysOptions({ path: { customerId } }))

  const revoke = useMutation({
    ...revokeApiKeyMutation(),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: listApiKeysQueryKey({ path: { customerId } }),
      })
      setRevokeTarget(null)
    },
  })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-[13px] font-semibold text-ink">API keys</h2>
        {canMutate(me) && (
          <Button variant="primary" size="sm" onClick={() => setCreateOpen(true)}>
            <Plus aria-hidden />
            Create key
          </Button>
        )}
      </div>

      {keysQuery.isPending && (
        <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}
      {keysQuery.isError && (
        <ErrorState title="Could not load API keys" onRetry={() => void keysQuery.refetch()} />
      )}
      {keysQuery.isSuccess &&
        (keysQuery.data.apiKeys.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
            <KeyRound className="size-5 text-ink-3" />
            <div className="text-sm font-semibold text-ink">No API keys</div>
            <p className="max-w-md text-[13px] text-ink-2">
              This customer cannot ingest telemetry without a key.
              {canMutate(me) ? ' Create one to enable OTLP export.' : ''}
            </p>
          </div>
        ) : (
          <ApiKeysTable
            keys={keysQuery.data.apiKeys}
            canRevoke={canMutate(me)}
            onRevoke={setRevokeTarget}
          />
        ))}

      <CreateApiKeyDialog
        customerId={customerId}
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={setCreatedKey}
      />
      <SecretDialog apiKey={createdKey} onClose={() => setCreatedKey(null)} />
      <ConfirmDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRevokeTarget(null)
        }}
        title={`Revoke ${revokeTarget?.name ?? 'this key'}?`}
        description="Revocation is permanent. The gateway stops accepting this key within about 60 seconds; exporters still using it will be refused."
        confirmLabel="Revoke key"
        destructive
        pending={revoke.isPending}
        onConfirm={() => {
          if (revokeTarget) {
            revoke.mutate({ path: { customerId, keyId: revokeTarget.id } })
          }
        }}
      />
    </div>
  )
}

function keyState(key: ApiKey): 'revoked' | 'expired' | 'active' {
  if (key.revokedAt) return 'revoked'
  if (key.expiresAt && new Date(key.expiresAt).getTime() < Date.now()) return 'expired'
  return 'active'
}

function ApiKeysTable({
  keys,
  canRevoke,
  onRevoke,
}: {
  keys: ApiKey[]
  canRevoke: boolean
  onRevoke: (key: ApiKey) => void
}) {
  return (
    <section className="rounded-lg border border-line bg-surface">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Name</TableHead>
            <TableHead>Key prefix</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Expires</TableHead>
            <TableHead>Last used</TableHead>
            <TableHead>Status</TableHead>
            {canRevoke && <TableHead className="text-right">Actions</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {keys.map((key) => {
            const state = keyState(key)
            return (
              <TableRow key={key.id} className={cn(state === 'revoked' && 'opacity-60')}>
                <TableCell className="font-medium text-ink">{key.name}</TableCell>
                <TableCell>
                  <code className="font-mono text-xs text-ink-2">{key.keyPrefix}</code>
                </TableCell>
                <TableCell className="text-xs text-ink-2" title={formatDateTime(key.createdAt)}>
                  {formatDate(key.createdAt)}
                </TableCell>
                <TableCell className="text-xs text-ink-2">
                  {key.expiresAt ? formatDate(key.expiresAt) : 'Never'}
                </TableCell>
                <TableCell className="text-xs text-ink-2">
                  {key.lastUsedAt ? formatRelative(key.lastUsedAt) : 'Never'}
                </TableCell>
                <TableCell>
                  {state === 'revoked' && (
                    <Badge dot variant="danger">
                      Revoked
                    </Badge>
                  )}
                  {state === 'expired' && (
                    <Badge dot variant="warn">
                      Expired
                    </Badge>
                  )}
                  {state === 'active' && (
                    <Badge dot variant="ok">
                      Active
                    </Badge>
                  )}
                </TableCell>
                {canRevoke && (
                  <TableCell className="text-right">
                    {state !== 'revoked' && (
                      <Button variant="danger" size="sm" onClick={() => onRevoke(key)}>
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                )}
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </section>
  )
}

function CustomerSettingsTab({ customer }: { customer: Customer }) {
  const me = useMe()
  const editable = canMutate(me)
  const queryClient = useQueryClient()

  const [rateLimit, setRateLimit] = useState(
    customer.rateLimitItemsPerSec != null ? String(customer.rateLimitItemsPerSec) : '',
  )
  const [retention, setRetention] = useState(
    customer.retentionDays != null ? String(customer.retentionDays) : '',
  )

  const update = useMutation({
    ...updateCustomerMutation(),
    onSuccess: (updated) => {
      queryClient.setQueryData(getCustomerQueryKey({ path: { customerId: customer.id } }), updated)
      void queryClient.invalidateQueries({ queryKey: listCustomersQueryKey() })
      toast('Customer settings saved')
    },
    onError: (error) => toast(apiErrorMessage(error, 'Could not save settings'), 'danger'),
  })

  // null clears the override; the PATCH body distinguishes present-null from absent.
  const parseOptional = (raw: string): number | null => {
    const trimmed = raw.trim()
    if (trimmed === '') return null
    const n = Number(trimmed)
    return Number.isFinite(n) ? n : null
  }

  const saveQuota = () =>
    update.mutate({
      path: { customerId: customer.id },
      body: { rateLimitItemsPerSec: parseOptional(rateLimit) },
    })
  const saveRetention = () => {
    const days = parseOptional(retention)
    if (days != null && (days < 1 || days > 30)) {
      toast('Retention must be between 1 and 30 days', 'danger')
      return
    }
    update.mutate({ path: { customerId: customer.id }, body: { retentionDays: days } })
  }

  return (
    <div className="flex max-w-xl flex-col gap-4">
      <section className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
        <Label htmlFor="rate-limit">Ingest quota (items/sec)</Label>
        <div className="flex items-center gap-2">
          <Input
            id="rate-limit"
            type="number"
            min={1}
            className="max-w-40"
            placeholder="unlimited"
            value={rateLimit}
            disabled={!editable}
            onChange={(e) => setRateLimit(e.target.value)}
          />
          {editable && (
            <Button variant="outline" size="sm" onClick={saveQuota} disabled={update.isPending}>
              Save
            </Button>
          )}
        </div>
        <p className="text-[11px] text-ink-3">
          Enforced at the gateway within ~30s. Over-quota requests get a retryable
          429 / RESOURCE_EXHAUSTED. Leave blank for unlimited.
        </p>
      </section>

      <section className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
        <Label htmlFor="retention">Retention (days)</Label>
        <div className="flex items-center gap-2">
          <Input
            id="retention"
            type="number"
            min={1}
            max={30}
            className="max-w-40"
            placeholder="30 (default)"
            value={retention}
            disabled={!editable}
            onChange={(e) => setRetention(e.target.value)}
          />
          {editable && (
            <Button variant="outline" size="sm" onClick={saveRetention} disabled={update.isPending}>
              Save
            </Button>
          )}
        </div>
        <p className="text-[11px] text-ink-3">
          Telemetry older than this is deleted by the nightly sweep. Leave blank to keep the
          global 30-day retention (1–30 days).
        </p>
      </section>
    </div>
  )
}

function CustomerDetailSkeleton() {
  return (
    <div className="flex flex-col gap-5">
      <Skeleton className="h-4 w-24" />
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-4 w-96" />
      <Skeleton className="h-9 w-full" />
      <Skeleton className="h-52 w-full" />
      <Skeleton className="h-52 w-full" />
    </div>
  )
}
