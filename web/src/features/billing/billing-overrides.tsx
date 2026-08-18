import { useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import {
  deleteBillingOverrideMutation,
  listBillingOverridesOptions,
  listBillingOverridesQueryKey,
  listCustomersOptions,
  setBillingOverrideMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import type { BillingOverride } from '@/api/generated'
import { formatMicro, microToUnit, unitToMicro } from '@/lib/format'
import { apiErrorMessage } from '@/lib/api-error'
import { ErrorState } from '@/components/error-state'
import { toast } from '@/components/toaster'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogClose,
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

/**
 * Per-customer price overrides. A row here supersedes the global price list for
 * one customer; a blank price inherits the global rate for that dimension.
 */
export function BillingOverridesCard({ currency }: { currency: string }) {
  const overrides = useQuery(listBillingOverridesOptions())
  const [dialogFor, setDialogFor] = useState<BillingOverride | 'new' | null>(null)

  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">Per-customer overrides</h2>
          <p className="text-xs text-ink-2">
            Custom rates for specific customers. A blank price inherits the global rate.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setDialogFor('new')}>
          <Plus aria-hidden />
          Add override
        </Button>
      </div>

      {overrides.isPending && <Skeleton className="mt-3 h-20 w-full" />}
      {overrides.isError && (
        <div className="mt-3">
          <ErrorState title="Could not load overrides" onRetry={() => void overrides.refetch()} />
        </div>
      )}
      {overrides.isSuccess &&
        (overrides.data.overrides.length === 0 ? (
          <p className="mt-3 text-[13px] text-ink-3">
            No overrides — every customer bills at the global rate.
          </p>
        ) : (
          <div className="mt-3 overflow-x-auto rounded-md border border-line">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Customer</TableHead>
                  <TableHead className="text-right">Price per GiB</TableHead>
                  <TableHead className="text-right">Price per million items</TableHead>
                  <TableHead className="w-0" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {overrides.data.overrides.map((o) => (
                  <OverrideRow
                    key={o.customerId}
                    override={o}
                    currency={currency}
                    onEdit={() => setDialogFor(o)}
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        ))}

      {dialogFor !== null && (
        <OverrideDialog
          currency={currency}
          existing={dialogFor === 'new' ? null : dialogFor}
          takenCustomerIds={new Set((overrides.data?.overrides ?? []).map((o) => o.customerId))}
          onClose={() => setDialogFor(null)}
        />
      )}
    </div>
  )
}

function priceCell(micro: number | null | undefined, currency: string) {
  return micro == null ? (
    <span className="text-ink-3">global</span>
  ) : (
    <span className="font-mono text-ink">{formatMicro(micro, currency)}</span>
  )
}

function OverrideRow({
  override,
  currency,
  onEdit,
}: {
  override: BillingOverride
  currency: string
  onEdit: () => void
}) {
  const queryClient = useQueryClient()
  const remove = useMutation({
    ...deleteBillingOverrideMutation(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: listBillingOverridesQueryKey() })
      void queryClient.invalidateQueries({ queryKey: [{ _id: 'getBillingStatement' }] })
      toast(`Removed override for ${override.customerName}`)
    },
    onError: (err) => toast(apiErrorMessage(err, 'Could not remove the override')),
  })

  return (
    <TableRow>
      <TableCell className="text-ink">{override.customerName || override.customerId}</TableCell>
      <TableCell className="text-right text-[13px]">
        {priceCell(override.pricePerGibMicro, currency)}
      </TableCell>
      <TableCell className="text-right text-[13px]">
        {priceCell(override.pricePerMillionItemsMicro, currency)}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1">
          <Button variant="ghost" size="icon" onClick={onEdit} aria-label="Edit override">
            <Pencil aria-hidden />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            disabled={remove.isPending}
            onClick={() => remove.mutate({ path: { customerId: override.customerId } })}
            aria-label="Remove override"
          >
            <Trash2 aria-hidden />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

/** Empty string = inherit global; otherwise a non-negative micro amount. */
function parsePrice(value: string): { micro: number | null; ok: boolean } {
  if (value.trim() === '') return { micro: null, ok: true }
  const n = Number(value)
  if (!Number.isFinite(n) || n < 0) return { micro: null, ok: false }
  return { micro: unitToMicro(n), ok: true }
}

function OverrideDialog({
  currency,
  existing,
  takenCustomerIds,
  onClose,
}: {
  currency: string
  existing: BillingOverride | null
  takenCustomerIds: Set<string>
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const customers = useQuery(listCustomersOptions())

  const [customerId, setCustomerId] = useState(existing?.customerId ?? '')
  const [gib, setGib] = useState(
    existing?.pricePerGibMicro != null ? String(microToUnit(existing.pricePerGibMicro)) : '',
  )
  const [items, setItems] = useState(
    existing?.pricePerMillionItemsMicro != null
      ? String(microToUnit(existing.pricePerMillionItemsMicro))
      : '',
  )
  const [error, setError] = useState<string | null>(null)

  // When adding, offer only customers that do not already have an override.
  const selectable = useMemo(
    () => (customers.data?.customers ?? []).filter((c) => !takenCustomerIds.has(c.id)),
    [customers.data, takenCustomerIds],
  )

  const save = useMutation({
    ...setBillingOverrideMutation(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: listBillingOverridesQueryKey() })
      void queryClient.invalidateQueries({ queryKey: [{ _id: 'getBillingStatement' }] })
      toast('Override saved')
      onClose()
    },
    onError: (err) => setError(apiErrorMessage(err, 'Could not save the override')),
  })

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError(null)
    if (customerId === '') {
      setError('Choose a customer')
      return
    }
    const g = parsePrice(gib)
    const i = parsePrice(items)
    if (!g.ok || !i.ok) {
      setError('Enter valid, non-negative prices (or leave blank to inherit)')
      return
    }
    if (g.micro === null && i.micro === null) {
      setError('Set at least one price, or remove the override to use global pricing')
      return
    }
    save.mutate({
      path: { customerId },
      body: { pricePerGibMicro: g.micro, pricePerMillionItemsMicro: i.micro },
    })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{existing ? 'Edit override' : 'Add override'}</DialogTitle>
          <DialogDescription>
            Set a custom rate for one customer. Leave a field blank to inherit the global rate for
            that dimension.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="override-customer">Customer</Label>
            {existing ? (
              <div className="flex items-center gap-2 text-[13px] text-ink">
                {existing.customerName || existing.customerId}
                <Badge variant="accent">override</Badge>
              </div>
            ) : (
              <Select
                id="override-customer"
                value={customerId}
                onChange={(e) => setCustomerId(e.target.value)}
                disabled={customers.isPending}
              >
                <option value="" disabled>
                  {customers.isPending ? 'Loading…' : 'Select a customer'}
                </option>
                {selectable.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </Select>
            )}
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="override-gib">Price per GiB ({currency})</Label>
              <Input
                id="override-gib"
                type="number"
                min="0"
                step="0.01"
                placeholder="global"
                value={gib}
                onChange={(e) => setGib(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="override-items">Price per million items ({currency})</Label>
              <Input
                id="override-items"
                type="number"
                min="0"
                step="0.01"
                placeholder="global"
                value={items}
                onChange={(e) => setItems(e.target.value)}
              />
            </div>
          </div>
          {error && (
            <p role="alert" className="text-xs text-danger">
              {error}
            </p>
          )}
          <DialogFooter className="mt-1">
            <DialogClose asChild>
              <Button variant="ghost" type="button" disabled={save.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : 'Save override'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
