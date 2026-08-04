import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Pencil, Plus, Trash2, Webhook as WebhookIcon, XCircle } from 'lucide-react'
import {
  createWebhookMutation,
  deleteWebhookMutation,
  listWebhooksOptions,
  listWebhooksQueryKey,
  testWebhookMutation,
  updateWebhookMutation,
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
import { cn } from '@/lib/utils'
import type { Webhook, WebhookEvent, WebhookType } from '@/api/generated'

const EVENTS: { value: WebhookEvent; label: string; hint: string }[] = [
  { value: 'agent_offline', label: 'Agent offline', hint: 'An edge agent disconnected' },
  { value: 'agent_config_failed', label: 'Config failed', hint: 'A pushed config was rejected' },
  { value: 'agent_unhealthy', label: 'Agent unhealthy', hint: 'A component reported unhealthy' },
]

const EVENT_LABEL: Record<string, string> = Object.fromEntries(EVENTS.map((e) => [e.value, e.label]))

// Per-type presentation + field rules for the channel dialog. `secret` drives
// whether the secret field shows and whether it is mandatory; `urlRequired`
// distinguishes bring-your-own-URL channels (webhook/slack) from fixed-endpoint
// vendors (pagerduty/opsgenie, where the URL is optional and defaults).
type ChannelMeta = {
  label: string
  hint: string
  urlRequired: boolean
  urlLabel: string
  urlPlaceholder: string
  urlHelp: string
  secret: 'none' | 'optional' | 'required'
  secretLabel: string
  secretPlaceholder: string
  secretHelp: string
}

const CHANNEL_META: Record<WebhookType, ChannelMeta> = {
  webhook: {
    label: 'Webhook',
    hint: 'HMAC-signed JSON POST to your endpoint',
    urlRequired: true,
    urlLabel: 'URL',
    urlPlaceholder: 'https://alerts.example.com/otel-fleet',
    urlHelp: 'https:// required (http:// only for localhost).',
    secret: 'optional',
    secretLabel: 'Signing secret',
    secretPlaceholder: 'optional',
    secretHelp: 'Optional — deliveries are HMAC-SHA256 signed (X-Otelfleet-Signature).',
  },
  slack: {
    label: 'Slack',
    hint: 'Message posted to a Slack channel',
    urlRequired: true,
    urlLabel: 'Slack incoming webhook URL',
    urlPlaceholder: 'https://hooks.slack.com/services/…',
    urlHelp: 'From Slack → Incoming Webhooks (https://hooks.slack.com/…).',
    secret: 'none',
    secretLabel: '',
    secretPlaceholder: '',
    secretHelp: '',
  },
  pagerduty: {
    label: 'PagerDuty',
    hint: 'Triggers/resolves a PagerDuty incident (Events API v2)',
    urlRequired: false,
    urlLabel: 'URL (optional)',
    urlPlaceholder: 'https://events.pagerduty.com/v2/enqueue (default)',
    urlHelp: 'Leave blank to use the default PagerDuty endpoint.',
    secret: 'required',
    secretLabel: 'Integration routing key',
    secretPlaceholder: 'from a service → Integrations → Events API v2',
    secretHelp: 'The Events API v2 integration key for the target PagerDuty service.',
  },
  opsgenie: {
    label: 'Opsgenie',
    hint: 'Creates/closes an Opsgenie alert (Alert API)',
    urlRequired: false,
    urlLabel: 'URL (optional)',
    urlPlaceholder: 'https://api.opsgenie.com/v2/alerts (default)',
    urlHelp: 'Leave blank for the US region; set https://api.eu.opsgenie.com/v2/alerts for EU.',
    secret: 'required',
    secretLabel: 'GenieKey API key',
    secretPlaceholder: 'an Opsgenie API integration key',
    secretHelp: 'An Opsgenie API integration key (sent as Authorization: GenieKey …).',
  },
}

const CHANNEL_TYPES: { value: WebhookType; label: string; hint: string }[] = (
  Object.keys(CHANNEL_META) as WebhookType[]
).map((value) => ({ value, label: CHANNEL_META[value].label, hint: CHANNEL_META[value].hint }))

const TYPE_LABEL: Record<WebhookType, string> = Object.fromEntries(
  (Object.keys(CHANNEL_META) as WebhookType[]).map((v) => [v, CHANNEL_META[v].label]),
) as Record<WebhookType, string>

interface TestResult {
  ok: boolean
  message: string
}

export function WebhooksTab() {
  const queryClient = useQueryClient()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Webhook | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Webhook | null>(null)
  const [results, setResults] = useState<Record<string, TestResult>>({})
  const [testingId, setTestingId] = useState<string | null>(null)

  const query = useQuery(listWebhooksOptions())
  const invalidate = () => queryClient.invalidateQueries({ queryKey: listWebhooksQueryKey() })

  const toggle = useMutation({
    ...updateWebhookMutation(),
    onSuccess: () => void invalidate(),
    onError: (error) => toast(apiErrorMessage(error, 'Could not update the webhook'), 'danger'),
  })

  const remove = useMutation({
    ...deleteWebhookMutation(),
    onSuccess: () => {
      void invalidate()
      setDeleteTarget(null)
      toast('Webhook deleted')
    },
    onError: (error) => {
      setDeleteTarget(null)
      toast(apiErrorMessage(error, 'Could not delete the webhook'), 'danger')
    },
  })

  const test = useMutation({
    ...testWebhookMutation(),
    onMutate: (variables) => setTestingId(variables.path.webhookId),
    onSuccess: (result, variables) =>
      setResults((prev) => ({ ...prev, [variables.path.webhookId]: result })),
    onError: (error, variables) =>
      setResults((prev) => ({
        ...prev,
        [variables.path.webhookId]: { ok: false, message: apiErrorMessage(error, 'Delivery failed') },
      })),
    onSettled: () => setTestingId(null),
  })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">Notification channels</h2>
          <p className="text-xs text-ink-2">
            Get notified on fleet events via a generic webhook or Slack. Generic webhook deliveries
            carry an
            <code className="mx-1 font-mono">X-Otelfleet-Signature</code>
            header when a signing secret is set.
          </p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setDialogOpen(true)}>
          <Plus aria-hidden />
          Add channel
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
        <ErrorState title="Could not load webhooks" onRetry={() => void query.refetch()} />
      )}
      {query.isSuccess &&
        (query.data.webhooks.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
            <WebhookIcon className="size-5 text-ink-3" />
            <div className="text-sm font-semibold text-ink">No notification channels</div>
            <p className="max-w-md text-[13px] text-ink-2">
              Add a channel to get notified — via a generic webhook or Slack — when an edge agent
              goes offline, fails to apply a config, or reports unhealthy.
            </p>
          </div>
        ) : (
          <section className="rounded-lg border border-line bg-surface">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>URL</TableHead>
                  <TableHead>Events</TableHead>
                  <TableHead>Enabled</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.data.webhooks.map((wh) => {
                  const result = results[wh.id]
                  return (
                    <TableRow key={wh.id}>
                      <TableCell>
                        <span className="flex items-center gap-2 font-medium text-ink">
                          {wh.name}
                          {wh.type === 'webhook' && wh.hasSecret && (
                            <Badge title="Deliveries are HMAC-SHA256 signed">signed</Badge>
                          )}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Badge variant={wh.type === 'slack' ? 'accent' : 'neutral'}>
                          {TYPE_LABEL[wh.type]}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {wh.url ? (
                          <code
                            className="block max-w-56 truncate font-mono text-xs text-ink-2"
                            title={wh.url}
                          >
                            {wh.url}
                          </code>
                        ) : (
                          <span className="text-xs text-ink-3">default endpoint</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {wh.events.map((e) => (
                            <Badge key={e} variant="accent">
                              {EVENT_LABEL[e] ?? e}
                            </Badge>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Switch
                          aria-label={`${wh.name} enabled`}
                          checked={wh.enabled}
                          onCheckedChange={(enabled) =>
                            toggle.mutate({ path: { webhookId: wh.id }, body: { enabled } })
                          }
                        />
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex flex-col items-end gap-1">
                          <div className="flex items-center justify-end gap-1">
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={testingId === wh.id}
                              onClick={() => test.mutate({ path: { webhookId: wh.id } })}
                            >
                              {testingId === wh.id ? 'Testing…' : 'Test'}
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              aria-label={`Edit ${wh.name}`}
                              onClick={() => setEditTarget(wh)}
                            >
                              <Pencil />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 hover:text-danger"
                              aria-label={`Delete ${wh.name}`}
                              onClick={() => setDeleteTarget(wh)}
                            >
                              <Trash2 />
                            </Button>
                          </div>
                          {result && (
                            <span
                              role="status"
                              className={cn(
                                'inline-flex max-w-72 items-center gap-1 text-right text-[11px]',
                                result.ok ? 'text-ok' : 'text-danger',
                              )}
                            >
                              {result.ok ? (
                                <CheckCircle2 aria-hidden className="size-3 shrink-0" />
                              ) : (
                                <XCircle aria-hidden className="size-3 shrink-0" />
                              )}
                              <span className="min-w-0">{result.message}</span>
                            </span>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </section>
        ))}

      <WebhookDialog
        open={dialogOpen || editTarget !== null}
        webhook={editTarget}
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
        title={`Delete ${deleteTarget?.name ?? 'this webhook'}?`}
        description="No further events will be delivered to this endpoint."
        confirmLabel="Delete webhook"
        destructive
        pending={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate({ path: { webhookId: deleteTarget.id } })
        }}
      />
    </div>
  )
}

function WebhookDialog({
  open,
  webhook,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  webhook: Webhook | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const editing = webhook !== null
  const [type, setType] = useState<WebhookType>('webhook')
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [events, setEvents] = useState<WebhookEvent[]>([])
  const [secret, setSecret] = useState('')
  const [removeSecret, setRemoveSecret] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const meta = CHANNEL_META[type]
  const hasSecretField = meta.secret !== 'none'

  // Reset the form whenever the dialog opens for a different target.
  const [seededFor, setSeededFor] = useState<string | null>(null)
  const key = webhook?.id ?? '__new__'
  if (open && seededFor !== key) {
    setType(webhook?.type ?? 'webhook')
    setName(webhook?.name ?? '')
    setUrl(webhook?.url ?? '')
    setEvents(webhook?.events ?? [])
    setSecret('')
    setRemoveSecret(false)
    setError(null)
    setSeededFor(key)
  }
  if (!open && seededFor !== null) setSeededFor(null)

  const create = useMutation({
    ...createWebhookMutation(),
    onSuccess: () => {
      onSaved()
      toast('Webhook created')
      onOpenChange(false)
    },
    onError: (err) => setError(apiErrorMessage(err, 'Could not create the webhook')),
  })
  const update = useMutation({
    ...updateWebhookMutation(),
    onSuccess: () => {
      onSaved()
      toast('Webhook updated')
      onOpenChange(false)
    },
    onError: (err) => setError(apiErrorMessage(err, 'Could not update the webhook')),
  })

  const pending = create.isPending || update.isPending

  const submit = () => {
    setError(null)
    if (events.length === 0) {
      setError('Select at least one event')
      return
    }
    if (meta.urlRequired && !url) {
      setError('URL is required')
      return
    }
    // PagerDuty/Opsgenie need their key; on edit an existing stored secret is fine.
    if (meta.secret === 'required' && !secret && !(editing && webhook?.hasSecret)) {
      setError(`${meta.secretLabel} is required`)
      return
    }
    if (editing && webhook) {
      const body: Record<string, unknown> = { type, name, url, events }
      // Channels without a secret field (Slack) never send one.
      if (hasSecretField) {
        if (removeSecret) body.secret = ''
        else if (secret) body.secret = secret
      }
      update.mutate({ path: { webhookId: webhook.id }, body })
    } else {
      create.mutate({
        body: { type, name, url, events, ...(hasSecretField ? { secret: secret || null } : {}) },
      })
    }
  }

  const toggleEvent = (value: WebhookEvent) =>
    setEvents((prev) => (prev.includes(value) ? prev.filter((e) => e !== value) : [...prev, value]))

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{editing ? 'Edit channel' : 'Add channel'}</DialogTitle>
          <DialogDescription>
            otel-fleet notifies this channel on the selected fleet events.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <fieldset className="flex flex-col gap-1.5">
            <legend className="text-xs font-medium text-ink-2 select-none">Channel type</legend>
            <div className="mt-1.5 grid grid-cols-2 gap-2">
              {CHANNEL_TYPES.map((option) => (
                <label
                  key={option.value}
                  className="flex cursor-pointer items-start gap-2.5 rounded-md border border-line px-3 py-2 transition-colors hover:bg-surface-2 has-checked:border-accent/50 has-checked:bg-accent/5"
                >
                  <input
                    type="radio"
                    name="wh-type"
                    value={option.value}
                    checked={type === option.value}
                    onChange={() => setType(option.value)}
                    className="mt-0.5 accent-(--accent)"
                  />
                  <span className="flex min-w-0 flex-col">
                    <span className="text-[13px] font-medium text-ink">{option.label}</span>
                    <span className="text-xs text-ink-3">{option.hint}</span>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="wh-name">Name</Label>
            <Input id="wh-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="wh-url">{meta.urlLabel}</Label>
            <Input
              id="wh-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder={meta.urlPlaceholder}
            />
            <p className="text-[11px] text-ink-3">{meta.urlHelp}</p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Events</Label>
            <div className="flex flex-col gap-1.5">
              {EVENTS.map((e) => (
                <label
                  key={e.value}
                  className="flex cursor-pointer items-center gap-2 text-[13px] text-ink"
                >
                  <input
                    type="checkbox"
                    className="accent-accent"
                    checked={events.includes(e.value)}
                    onChange={() => toggleEvent(e.value)}
                  />
                  <span>{e.label}</span>
                  <span className="text-ink-3">— {e.hint}</span>
                </label>
              ))}
            </div>
          </div>
          {hasSecretField && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="wh-secret">
                {meta.secretLabel}
                {meta.secret === 'required' && <span className="text-danger"> *</span>}
                {editing && ' (leave blank to keep)'}
              </Label>
              <Input
                id="wh-secret"
                type="password"
                value={secret}
                disabled={removeSecret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder={editing && webhook?.hasSecret ? '•••••• (stored)' : meta.secretPlaceholder}
              />
              <p className="text-[11px] text-ink-3">{meta.secretHelp}</p>
              {/* Only optional-secret channels (generic webhook) can drop signing. */}
              {meta.secret === 'optional' && editing && webhook?.hasSecret && (
                <label className="flex items-center gap-2 text-[11px] text-ink-2">
                  <input
                    type="checkbox"
                    className="accent-accent"
                    checked={removeSecret}
                    onChange={(e) => setRemoveSecret(e.target.checked)}
                  />
                  Remove signing (send unsigned)
                </label>
              )}
            </div>
          )}
          {error && <p className="text-[13px] text-danger">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={submit}
            disabled={pending || !name || (meta.urlRequired && !url)}
          >
            {pending ? 'Saving…' : editing ? 'Save changes' : 'Add channel'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
