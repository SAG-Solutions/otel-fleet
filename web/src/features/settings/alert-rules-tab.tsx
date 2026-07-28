import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { BellRing, Pencil, Plus, Trash2 } from 'lucide-react'
import {
  createAlertRuleMutation,
  deleteAlertRuleMutation,
  listAlertRulesOptions,
  listAlertRulesQueryKey,
  listCustomersOptions,
  listWebhooksOptions,
  updateAlertRuleMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import { apiErrorMessage } from '@/lib/api-error'
import { toast } from '@/components/toaster'
import { ErrorState } from '@/components/error-state'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { AlertComparison, AlertMetric, AlertRule } from '@/api/generated'

const METRICS: { value: AlertMetric; label: string }[] = [
  { value: 'ingest_items', label: 'Ingest items' },
  { value: 'error_logs', label: 'Error logs' },
]
const METRIC_LABEL: Record<AlertMetric, string> = {
  ingest_items: 'Ingest items',
  error_logs: 'Error logs',
}

const COMPARISONS: { value: AlertComparison; label: string }[] = [
  { value: 'below', label: 'below' },
  { value: 'above', label: 'above' },
]

const WINDOWS: { value: number; label: string }[] = [
  { value: 60, label: '1m' },
  { value: 300, label: '5m' },
  { value: 900, label: '15m' },
  { value: 3600, label: '1h' },
]

function windowLabel(seconds: number): string {
  return WINDOWS.find((w) => w.value === seconds)?.label ?? `${seconds}s`
}

function conditionText(rule: AlertRule): string {
  return `${METRIC_LABEL[rule.metric]} ${rule.comparison} ${rule.threshold} over ${windowLabel(rule.windowSeconds)}`
}

export function AlertRulesTab() {
  const queryClient = useQueryClient()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<AlertRule | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AlertRule | null>(null)

  const query = useQuery(listAlertRulesOptions())
  const customersQuery = useQuery(listCustomersOptions())
  const invalidate = () => queryClient.invalidateQueries({ queryKey: listAlertRulesQueryKey() })

  const customerName = (id: string | null | undefined): string => {
    if (!id) return 'All customers'
    return customersQuery.data?.customers.find((c) => c.id === id)?.name ?? 'Unknown customer'
  }

  const toggle = useMutation({
    ...updateAlertRuleMutation(),
    onSuccess: () => void invalidate(),
    onError: (error) => toast(apiErrorMessage(error, 'Could not update the alert rule'), 'danger'),
  })

  const remove = useMutation({
    ...deleteAlertRuleMutation(),
    onSuccess: () => {
      void invalidate()
      setDeleteTarget(null)
      toast('Alert rule deleted')
    },
    onError: (error) => {
      setDeleteTarget(null)
      toast(apiErrorMessage(error, 'Could not delete the alert rule'), 'danger')
    },
  })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">Alert rules</h2>
          <p className="text-xs text-ink-2">
            Fire a notification channel when an ingest or error-log metric crosses a threshold over
            a rolling window.
          </p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setDialogOpen(true)}>
          <Plus aria-hidden />
          New rule
        </Button>
      </div>

      {query.isPending && (
        <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
          {Array.from({ length: 2 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}
      {query.isError && (
        <ErrorState title="Could not load alert rules" onRetry={() => void query.refetch()} />
      )}
      {query.isSuccess &&
        (query.data.rules.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
            <BellRing className="size-5 text-ink-3" />
            <div className="text-sm font-semibold text-ink">No alert rules</div>
            <p className="max-w-md text-[13px] text-ink-2">
              Create a rule to get notified when ingest drops below — or error logs climb above — a
              threshold you set.
            </p>
          </div>
        ) : (
          <section className="rounded-lg border border-line bg-surface">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Name</TableHead>
                  <TableHead>Condition</TableHead>
                  <TableHead>Scope</TableHead>
                  <TableHead>Channels</TableHead>
                  <TableHead>Enabled</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.data.rules.map((rule) => (
                  <TableRow key={rule.id}>
                    <TableCell>
                      <span className="font-medium text-ink">{rule.name}</span>
                    </TableCell>
                    <TableCell>
                      <span className="text-[13px] text-ink-2">{conditionText(rule)}</span>
                    </TableCell>
                    <TableCell>
                      <Badge variant={rule.customerId ? 'accent' : 'neutral'}>
                        {customerName(rule.customerId)}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <span className="text-[13px] text-ink-2">
                        {rule.channelIds.length}{' '}
                        {rule.channelIds.length === 1 ? 'channel' : 'channels'}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Switch
                        aria-label={`${rule.name} enabled`}
                        checked={rule.enabled}
                        onCheckedChange={(enabled) =>
                          toggle.mutate({ path: { ruleId: rule.id }, body: { enabled } })
                        }
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          aria-label={`Edit ${rule.name}`}
                          onClick={() => setEditTarget(rule)}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 hover:text-danger"
                          aria-label={`Delete ${rule.name}`}
                          onClick={() => setDeleteTarget(rule)}
                        >
                          <Trash2 />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </section>
        ))}

      <AlertRuleDialog
        open={dialogOpen || editTarget !== null}
        rule={editTarget}
        onOpenChange={(open) => {
          if (!open) {
            setDialogOpen(false)
            setEditTarget(null)
          }
        }}
        onSaved={() => void invalidate()}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={`Delete ${deleteTarget?.name ?? 'this rule'}?`}
        description="This rule will stop evaluating and no further alerts will fire from it."
        confirmLabel="Delete rule"
        destructive
        pending={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate({ path: { ruleId: deleteTarget.id } })
        }}
      />
    </div>
  )
}

function AlertRuleDialog({
  open,
  rule,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  rule: AlertRule | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const editing = rule !== null
  const [name, setName] = useState('')
  const [metric, setMetric] = useState<AlertMetric>('ingest_items')
  const [comparison, setComparison] = useState<AlertComparison>('below')
  const [threshold, setThreshold] = useState('')
  const [windowSeconds, setWindowSeconds] = useState(300)
  const [customerId, setCustomerId] = useState<string>('')
  const [channelIds, setChannelIds] = useState<string[]>([])
  const [enabled, setEnabled] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const customersQuery = useQuery({ ...listCustomersOptions(), enabled: open })
  const webhooksQuery = useQuery({ ...listWebhooksOptions(), enabled: open })

  // Reset the form whenever the dialog opens for a different target.
  const [seededFor, setSeededFor] = useState<string | null>(null)
  const key = rule?.id ?? '__new__'
  if (open && seededFor !== key) {
    setName(rule?.name ?? '')
    setMetric(rule?.metric ?? 'ingest_items')
    setComparison(rule?.comparison ?? 'below')
    setThreshold(rule ? String(rule.threshold) : '')
    setWindowSeconds(rule?.windowSeconds ?? 300)
    setCustomerId(rule?.customerId ?? '')
    setChannelIds(rule?.channelIds ?? [])
    setEnabled(rule?.enabled ?? true)
    setError(null)
    setSeededFor(key)
  }
  if (!open && seededFor !== null) setSeededFor(null)

  const create = useMutation({
    ...createAlertRuleMutation(),
    onSuccess: () => {
      onSaved()
      toast('Alert rule created')
      onOpenChange(false)
    },
    onError: (err) => setError(apiErrorMessage(err, 'Could not create the alert rule')),
  })
  const update = useMutation({
    ...updateAlertRuleMutation(),
    onSuccess: () => {
      onSaved()
      toast('Alert rule updated')
      onOpenChange(false)
    },
    onError: (err) => setError(apiErrorMessage(err, 'Could not update the alert rule')),
  })

  const pending = create.isPending || update.isPending

  const submit = () => {
    setError(null)
    const thresholdValue = Number(threshold)
    if (!Number.isFinite(thresholdValue)) {
      setError('Enter a numeric threshold')
      return
    }
    if (windowSeconds < 60) {
      setError('The window must be at least 1 minute')
      return
    }
    if (editing && rule) {
      // AlertRuleUpdate has no customerId — scope is fixed after creation.
      update.mutate({
        path: { ruleId: rule.id },
        body: {
          name,
          metric,
          comparison,
          threshold: thresholdValue,
          windowSeconds,
          channelIds,
          enabled,
        },
      })
    } else {
      create.mutate({
        body: {
          name,
          metric,
          comparison,
          threshold: thresholdValue,
          windowSeconds,
          customerId: customerId || null,
          channelIds,
          enabled,
        },
      })
    }
  }

  const toggleChannel = (id: string) =>
    setChannelIds((prev) => (prev.includes(id) ? prev.filter((c) => c !== id) : [...prev, id]))

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{editing ? 'Edit alert rule' : 'New alert rule'}</DialogTitle>
          <DialogDescription>
            otelfleet evaluates this metric over the window and notifies the selected channels when
            the threshold is crossed.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ar-name">Name</Label>
            <Input id="ar-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="ar-metric">Metric</Label>
              <Select
                id="ar-metric"
                value={metric}
                onChange={(e) => setMetric(e.target.value as AlertMetric)}
              >
                {METRICS.map((m) => (
                  <option key={m.value} value={m.value}>
                    {m.label}
                  </option>
                ))}
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="ar-comparison">Comparison</Label>
              <Select
                id="ar-comparison"
                value={comparison}
                onChange={(e) => setComparison(e.target.value as AlertComparison)}
              >
                {COMPARISONS.map((c) => (
                  <option key={c.value} value={c.value}>
                    {c.label}
                  </option>
                ))}
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="ar-threshold">Threshold</Label>
              <Input
                id="ar-threshold"
                type="number"
                value={threshold}
                onChange={(e) => setThreshold(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="ar-window">Window</Label>
              <Select
                id="ar-window"
                value={String(windowSeconds)}
                onChange={(e) => setWindowSeconds(Number(e.target.value))}
              >
                {WINDOWS.map((w) => (
                  <option key={w.value} value={w.value}>
                    {w.label}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ar-scope">Customer scope</Label>
            <Select
              id="ar-scope"
              value={customerId}
              disabled={editing || customersQuery.isPending}
              onChange={(e) => setCustomerId(e.target.value)}
            >
              <option value="">All customers</option>
              {customersQuery.data?.customers.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </Select>
            {editing && (
              <p className="text-[11px] text-ink-3">Scope can't be changed after creation.</p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Notification channels</Label>
            {webhooksQuery.isPending ? (
              <div className="flex flex-col gap-1.5">
                {Array.from({ length: 2 }, (_, i) => (
                  <Skeleton key={i} className="h-8 w-full" />
                ))}
              </div>
            ) : webhooksQuery.isError ? (
              <p className="text-xs text-danger">Could not load notification channels.</p>
            ) : webhooksQuery.data && webhooksQuery.data.webhooks.length > 0 ? (
              <div className="flex max-h-40 flex-col gap-1 overflow-y-auto rounded-md border border-line p-1.5">
                {webhooksQuery.data.webhooks.map((wh) => (
                  <label
                    key={wh.id}
                    className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 transition-colors hover:bg-surface-2 has-checked:bg-accent/5"
                  >
                    <input
                      type="checkbox"
                      name="ar-channel"
                      value={wh.id}
                      checked={channelIds.includes(wh.id)}
                      onChange={() => toggleChannel(wh.id)}
                      className="accent-(--accent)"
                    />
                    <span className="truncate text-[13px] font-medium text-ink">{wh.name}</span>
                  </label>
                ))}
              </div>
            ) : (
              <p className="text-xs text-ink-3">
                No notification channels yet — add one on the Alerts tab.
              </p>
            )}
          </div>
          <label className="flex items-center gap-2.5">
            <Switch checked={enabled} onCheckedChange={setEnabled} aria-label="Rule enabled" />
            <span className="text-[13px] text-ink">Enabled</span>
          </label>
          {error && <p className="text-[13px] text-danger">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            Cancel
          </Button>
          <Button variant="primary" onClick={submit} disabled={pending || !name || threshold === ''}>
            {pending ? 'Saving…' : editing ? 'Save changes' : 'Create rule'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
