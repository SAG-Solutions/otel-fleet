import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Building2, KeyRound, Plus } from 'lucide-react'
import {
  getCustomerThroughputOptions,
  listApiKeysOptions,
  listApiKeysQueryKey,
  listCustomersOptions,
  revokeApiKeyMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import { DEFAULT_TIME_RANGE, RANGE_STEP, rangeToInterval, type TimeRange } from '@/lib/time-range'
import { formatDate, formatDateTime, formatRelative } from '@/lib/format'
import { SIGNALS } from '@/lib/chart-theme'
import { useMe, canMutate } from '@/hooks/use-me'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
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
import type { ApiKey, ApiKeyCreated, Customer, ThroughputPoint } from '@/api/generated'

/**
 * Tenant self-service overview for portal users. Reuses the exact building
 * blocks of the admin customer-detail page, but scoped to the user's own
 * customer(s) — `listCustomers` is filtered server-side to their grants.
 */
export function PortalOverview() {
  const customersQuery = useQuery(listCustomersOptions())
  const customers = customersQuery.data?.customers
  const [selectedId, setSelectedId] = useState<string | null>(null)

  // Default the switcher to the first granted customer once loaded.
  useEffect(() => {
    const first = customers?.[0]
    if (first && selectedId === null) {
      setSelectedId(first.id)
    }
  }, [customers, selectedId])

  if (customersQuery.isPending) return <PortalSkeleton />
  if (customersQuery.isError) {
    return (
      <ErrorState
        title="Could not load your tenant"
        onRetry={() => void customersQuery.refetch()}
      />
    )
  }

  if (!customers || customers.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-14 text-center">
        <Building2 className="size-5 text-ink-3" />
        <div className="text-sm font-semibold text-ink">No tenant available</div>
        <p className="max-w-md text-[13px] text-ink-2">
          Your account has no customer access yet. Ask an administrator to grant you a tenant.
        </p>
      </div>
    )
  }

  const selected = customers.find((c) => c.id === selectedId) ?? customers[0]
  if (!selected) return null

  return (
    <div className="flex flex-col gap-6">
      {customers.length > 1 && (
        <div className="flex max-w-xs flex-col gap-1.5">
          <Label htmlFor="portal-customer">Tenant</Label>
          <Select
            id="portal-customer"
            value={selected.id}
            onChange={(e) => setSelectedId(e.target.value)}
          >
            {customers.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
        </div>
      )}
      <CustomerPortal key={selected.id} customer={selected} />
    </div>
  )
}

function CustomerPortal({ customer }: { customer: Customer }) {
  return (
    <div className="flex flex-col gap-8">
      <PortalHeader customer={customer} />
      <ThroughputSection customerId={customer.id} />
      <ApiKeysSection customerId={customer.id} />
      <section className="flex flex-col gap-4">
        <h2 className="text-[13px] font-semibold text-ink">Edge agents</h2>
        <CustomerAgentsTab customerId={customer.id} />
      </section>
      <TerminalHint
        title="Send test data"
        body="Point an exporter at the gateway with one of your API keys — telemetrygen is the quickest smoke test."
        command={TELEMETRYGEN_COMMAND}
      />
    </div>
  )
}

function PortalHeader({ customer }: { customer: Customer }) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold text-ink">{customer.name}</h1>
        <StatusBadge status={customer.status} />
      </div>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-ink-2">
        <span className="inline-flex items-center gap-1">
          <span className="text-ink-3">client ID</span>
          <code className="font-mono">{customer.clientId}</code>
          <CopyButton value={customer.clientId} label="Copy client ID" />
        </span>
        <span>
          <span className="text-ink-3">since</span> {formatDate(customer.createdAt)}
        </span>
      </div>
    </div>
  )
}

function ThroughputSection({ customerId }: { customerId: string }) {
  const [range, setRange] = useState<TimeRange>(DEFAULT_TIME_RANGE)
  const interval = rangeToInterval(range)

  const throughputQuery = useQuery(
    getCustomerThroughputOptions({
      path: { customerId },
      query: { from: interval.from, to: interval.to, step: RANGE_STEP[range] },
    }),
  )

  return (
    <section className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-[13px] font-semibold text-ink">Ingest throughput</h2>
        <TimeRangePicker value={range} onChange={setRange} />
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
    </section>
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
        body="Nothing has been ingested in the selected window. Send a smoke signal with one of your API keys:"
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

function ApiKeysSection({ customerId }: { customerId: string }) {
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
    <section className="flex flex-col gap-4">
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
              You cannot ingest telemetry without a key.
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
    </section>
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

function PortalSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-4 w-96" />
      <Skeleton className="h-52 w-full" />
      <Skeleton className="h-52 w-full" />
    </div>
  )
}
