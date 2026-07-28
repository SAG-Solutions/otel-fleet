import { Fragment, useEffect, useState } from 'react'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { ChevronRight, ScrollText } from 'lucide-react'
import { listAuditLog } from '@/api/generated'
import { getMeOptions, listCustomersOptions } from '@/api/generated/@tanstack/react-query.gen'
import { isPortalUser } from '@/hooks/use-me'
import { isTimeRange, rangeToInterval, type TimeRange } from '@/lib/time-range'
import { formatDateTime, formatRelative } from '@/lib/format'
import { useDebouncedValue } from '@/hooks/use-debounced-value'
import { cn } from '@/lib/utils'
import {
  actionTone,
  entityTypeOptions,
  shortId,
  type ActionTone,
} from '@/features/audit/audit-meta'
import { AdminGate } from '@/components/admin-gate'
import { ErrorState } from '@/components/error-state'
import { TimeRangePicker } from '@/components/time-range-picker'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
import type { AuditEntry } from '@/api/generated'

const PAGE_SIZE = 50

interface AuditSearch {
  action?: string
  entityType?: string
  customer?: string
  range?: TimeRange
}

export const Route = createFileRoute('/_auth/audit')({
  validateSearch: (search: Record<string, unknown>): AuditSearch => ({
    action:
      typeof search.action === 'string' && search.action !== '' ? search.action : undefined,
    entityType:
      typeof search.entityType === 'string' && search.entityType !== ''
        ? search.entityType
        : undefined,
    customer:
      typeof search.customer === 'string' && search.customer !== '' ? search.customer : undefined,
    range: isTimeRange(search.range) ? search.range : undefined,
  }),
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(getMeOptions())
    if (isPortalUser(me)) throw redirect({ to: '/' })
  },
  component: AuditPage,
})

function AuditPage() {
  return (
    <AdminGate>
      <AuditPageBody />
    </AdminGate>
  )
}

function AuditPageBody() {
  const { action, entityType, customer, range = '7d' } = Route.useSearch()
  const navigate = Route.useNavigate()
  const interval = rangeToInterval(range)

  const auditQuery = useInfiniteQuery({
    queryKey: ['listAuditLog', { action, entityType, customer, ...interval }],
    queryFn: async ({ pageParam, signal }) => {
      const { data } = await listAuditLog({
        query: {
          limit: PAGE_SIZE,
          from: interval.from,
          to: interval.to,
          ...(action !== undefined ? { action } : {}),
          ...(entityType !== undefined ? { entityType } : {}),
          ...(customer !== undefined ? { customerId: customer } : {}),
          ...(pageParam !== undefined ? { beforeId: pageParam } : {}),
        },
        signal,
        throwOnError: true,
      })
      return data
    },
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (lastPage) => lastPage.nextBeforeId ?? undefined,
  })

  const entries = auditQuery.data?.pages.flatMap((page) => page.entries) ?? []
  const filtered = action !== undefined || entityType !== undefined || customer !== undefined

  const setFilter = (patch: Partial<AuditSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }), replace: true })

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-lg font-semibold text-ink">Audit log</h1>
        <p className="text-[13px] text-ink-2">
          Every mutating action against the control plane — who did what, when, to which entity.
        </p>
      </div>

      <FilterBar
        action={action}
        entityType={entityType}
        customer={customer}
        range={range}
        loadedEntityTypes={entries.map((e) => e.entityType)}
        onChange={setFilter}
        onClear={() =>
          void navigate({
            search: { range: range === '7d' ? undefined : range },
            replace: true,
          })
        }
      />

      {auditQuery.isPending && (
        <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}
      {auditQuery.isError && (
        <ErrorState title="Could not load the audit log" onRetry={() => void auditQuery.refetch()} />
      )}
      {auditQuery.isSuccess &&
        (entries.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
            <ScrollText className="size-5 text-ink-3" />
            <div className="text-sm font-semibold text-ink">
              {filtered ? 'No entries match the filters' : 'No audit entries in this range'}
            </div>
            <p className="max-w-md text-[13px] text-ink-2">
              {filtered
                ? 'Loosen the filters or widen the time range.'
                : 'Mutating actions (creates, updates, deletes) appear here as they happen.'}
            </p>
          </div>
        ) : (
          <>
            <AuditTable entries={entries} />
            <div className="flex items-center justify-center gap-3 pb-2">
              {auditQuery.hasNextPage ? (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={auditQuery.isFetchingNextPage}
                  onClick={() => void auditQuery.fetchNextPage()}
                >
                  {auditQuery.isFetchingNextPage ? 'Loading…' : 'Load more'}
                </Button>
              ) : (
                <span className="text-xs text-ink-3">
                  End of log for this range · {entries.length}{' '}
                  {entries.length === 1 ? 'entry' : 'entries'}
                </span>
              )}
            </div>
          </>
        ))}
    </div>
  )
}

function FilterBar({
  action,
  entityType,
  customer,
  range,
  loadedEntityTypes,
  onChange,
  onClear,
}: {
  action?: string
  entityType?: string
  customer?: string
  range: TimeRange
  loadedEntityTypes: string[]
  onChange: (patch: Partial<AuditSearch>) => void
  onClear: () => void
}) {
  const customersQuery = useQuery(listCustomersOptions())
  const [actionInput, setActionInput] = useState(action ?? '')
  const debouncedAction = useDebouncedValue(actionInput, 300)

  // Push the debounced text into the URL (which drives the query).
  useEffect(() => {
    const next = debouncedAction.trim() === '' ? undefined : debouncedAction.trim()
    if (next !== action) onChange({ action: next })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only the debounced text should trigger
  }, [debouncedAction])

  const filtered = action !== undefined || entityType !== undefined || customer !== undefined

  return (
    <div className="flex flex-wrap items-end gap-3">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="audit-action">Action</Label>
        <Input
          id="audit-action"
          className="w-44 font-mono"
          placeholder="e.g. delete"
          spellCheck={false}
          value={actionInput}
          onChange={(e) => setActionInput(e.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="audit-entity-type">Entity type</Label>
        <Select
          id="audit-entity-type"
          className="w-44"
          value={entityType ?? ''}
          onChange={(e) =>
            onChange({ entityType: e.target.value === '' ? undefined : e.target.value })
          }
        >
          <option value="">All types</option>
          {entityTypeOptions(loadedEntityTypes).map((type) => (
            <option key={type} value={type}>
              {type}
            </option>
          ))}
        </Select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="audit-customer">Customer</Label>
        <Select
          id="audit-customer"
          className="w-48"
          value={customer ?? ''}
          onChange={(e) =>
            onChange({ customer: e.target.value === '' ? undefined : e.target.value })
          }
        >
          <option value="">All customers</option>
          {(customersQuery.data?.customers ?? []).map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </Select>
      </div>
      <TimeRangePicker value={range} onChange={(next) => onChange({ range: next })} />
      {filtered && (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setActionInput('')
            onClear()
          }}
        >
          Clear filters
        </Button>
      )}
    </div>
  )
}

const TONE_VARIANT: Record<ActionTone, 'ok' | 'warn' | 'danger' | 'neutral'> = {
  ok: 'ok',
  warn: 'warn',
  danger: 'danger',
  neutral: 'neutral',
}

function AuditTable({ entries }: { entries: AuditEntry[] }) {
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set())

  const toggle = (id: number) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })

  return (
    <section className="rounded-lg border border-line bg-surface">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="w-8" aria-label="Details" />
            <TableHead>When</TableHead>
            <TableHead>Actor</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Entity</TableHead>
            <TableHead>Customer</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((entry) => {
            const hasPayload =
              entry.payload != null && Object.keys(entry.payload).length > 0
            const isOpen = expanded.has(entry.id)
            return (
              <Fragment key={entry.id}>
                <TableRow>
                  <TableCell className="w-8">
                    {hasPayload && (
                      <button
                        type="button"
                        aria-expanded={isOpen}
                        aria-label={`Payload of ${entry.action}`}
                        onClick={() => toggle(entry.id)}
                        className="inline-flex size-5 cursor-pointer items-center justify-center rounded text-ink-3 outline-none hover:bg-surface-2 hover:text-ink focus-visible:ring-2 focus-visible:ring-accent/70"
                      >
                        <ChevronRight
                          aria-hidden
                          className={cn('size-3.5 transition-transform', isOpen && 'rotate-90')}
                        />
                      </button>
                    )}
                  </TableCell>
                  <TableCell
                    className="text-xs whitespace-nowrap text-ink-2"
                    title={formatDateTime(entry.createdAt)}
                  >
                    {formatRelative(entry.createdAt)}
                  </TableCell>
                  <TableCell>
                    {entry.actorType === 'user' ? (
                      <code className="font-mono text-xs text-ink">
                        {entry.actorEmail ?? 'unknown user'}
                      </code>
                    ) : (
                      <Badge className="font-mono">{entry.actorType}</Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={TONE_VARIANT[actionTone(entry.action)]} className="font-mono">
                      {entry.action}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <EntityRef entry={entry} />
                  </TableCell>
                  <TableCell>
                    {entry.customerId != null && entry.customerName != null ? (
                      <Link
                        to="/customers/$customerId"
                        params={{ customerId: entry.customerId }}
                        className="rounded text-[13px] text-ink outline-none hover:text-accent hover:underline focus-visible:ring-2 focus-visible:ring-accent/70"
                      >
                        {entry.customerName}
                      </Link>
                    ) : (
                      <span className="text-xs text-ink-3">—</span>
                    )}
                  </TableCell>
                </TableRow>
                {hasPayload && isOpen && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={6} className="bg-surface-2/50 py-2">
                      <pre className="overflow-x-auto font-mono text-[11px] leading-4 whitespace-pre text-ink-2">
                        {JSON.stringify(entry.payload, null, 2)}
                      </pre>
                    </TableCell>
                  </TableRow>
                )}
              </Fragment>
            )
          })}
        </TableBody>
      </Table>
    </section>
  )
}

/** Entity type + short id; links into the console where a route exists. */
function EntityRef({ entry }: { entry: AuditEntry }) {
  const label = (
    <>
      <span className="text-ink-3">{entry.entityType}</span>{' '}
      <span title={entry.entityId}>{shortId(entry.entityId)}</span>
    </>
  )
  const linkClass =
    'rounded font-mono text-xs text-ink outline-none hover:text-accent hover:underline focus-visible:ring-2 focus-visible:ring-accent/70'

  switch (entry.entityType) {
    case 'customer':
      return (
        <Link to="/customers/$customerId" params={{ customerId: entry.entityId }} className={linkClass}>
          {label}
        </Link>
      )
    case 'pipeline':
      return (
        <Link to="/pipelines/$pipelineId" params={{ pipelineId: entry.entityId }} className={linkClass}>
          {label}
        </Link>
      )
    case 'agent':
      return (
        <Link to="/fleet/$agentId" params={{ agentId: entry.entityId }} className={linkClass}>
          {label}
        </Link>
      )
    case 'user':
      return (
        <Link to="/settings" search={{ tab: 'users' }} className={linkClass}>
          {label}
        </Link>
      )
    case 'auth_provider':
      return (
        <Link to="/settings" search={{ tab: 'sso' }} className={linkClass}>
          {label}
        </Link>
      )
    default:
      return <span className="font-mono text-xs text-ink-2">{label}</span>
  }
}
