import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info } from 'lucide-react'
import {
  inviteUserMutation,
  listCustomersOptions,
  listUsersQueryKey,
} from '@/api/generated/@tanstack/react-query.gen'
import { apiErrorMessage } from '@/lib/api-error'
import { toast } from '@/components/toaster'
import { CustomerMultiSelect } from '@/features/settings/customer-multi-select'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { Role } from '@/api/generated'

export const ROLE_DESCRIPTION: Record<Role, string> = {
  admin: 'Everything, plus users, SSO settings, and the audit log',
  operator: 'Manage customers, pipelines, and the fleet',
  viewer: 'Read-only access to the whole console',
}

const ROLES: readonly Role[] = ['admin', 'operator', 'viewer'] as const

export function InviteUserDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('operator')
  const [customerIds, setCustomerIds] = useState<string[]>([])
  const queryClient = useQueryClient()

  const customersQuery = useQuery(listCustomersOptions())

  const invite = useMutation({
    ...inviteUserMutation(),
    onSuccess: (user) => {
      void queryClient.invalidateQueries({ queryKey: listUsersQueryKey() })
      setEmail('')
      setRole('operator')
      setCustomerIds([])
      onOpenChange(false)
      toast(`Invited ${user.email}`)
    },
  })

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (email.trim() === '') return
    // Admins access every customer, so grants are never sent for them.
    const customerScope = role === 'admin' ? {} : { customerIds }
    invite.mutate({ body: { email: email.trim(), role, ...customerScope } })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Invite user</DialogTitle>
          <DialogDescription>
            The invite allow-lists this email — there is no invitation mail. The user signs in via
            SSO with that address.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="invite-email">Email</Label>
            <Input
              id="invite-email"
              type="email"
              required
              className="font-mono"
              placeholder="teammate@example.com"
              autoComplete="off"
              spellCheck={false}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </div>

          <fieldset className="flex flex-col gap-1.5">
            <legend className="text-xs font-medium text-ink-2 select-none">Role</legend>
            <div className="mt-1.5 flex flex-col gap-1">
              {ROLES.map((option) => (
                <label
                  key={option}
                  className="flex cursor-pointer items-start gap-2.5 rounded-md border border-line px-3 py-2 transition-colors hover:bg-surface-2 has-checked:border-accent/50 has-checked:bg-accent/5"
                >
                  <input
                    type="radio"
                    name="invite-role"
                    value={option}
                    checked={role === option}
                    onChange={() => setRole(option)}
                    className="mt-0.5 accent-(--accent)"
                  />
                  <span className="flex min-w-0 flex-col">
                    <span className="text-[13px] font-medium text-ink">{option}</span>
                    <span className="text-xs text-ink-3">{ROLE_DESCRIPTION[option]}</span>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>

          {role === 'admin' ? (
            <p className="text-xs text-ink-3">Admins access all customers.</p>
          ) : (
            <fieldset className="flex flex-col gap-1.5">
              <legend className="text-xs font-medium text-ink-2 select-none">
                Customer access
              </legend>
              <div className="mt-1.5">
                <CustomerMultiSelect
                  namePrefix="invite"
                  customers={customersQuery.data?.customers ?? []}
                  selected={customerIds}
                  onChange={setCustomerIds}
                  disabled={invite.isPending}
                  isLoading={customersQuery.isPending}
                  isError={customersQuery.isError}
                />
              </div>
              <p className="text-xs text-ink-3">Leave empty for access to all customers.</p>
              {customerIds.length >= 1 && (
                <p className="text-xs text-ink-3">
                  Scoping to a customer gives this user the self-service portal — limited to their
                  own tenant, no fleet-wide or admin surfaces.
                </p>
              )}
            </fieldset>
          )}

          <p className="flex items-start gap-1.5 text-xs text-ink-3">
            <Info aria-hidden className="mt-px size-3.5 shrink-0" />
            The role applies when the user first signs in through SSO. You can change it any time.
          </p>

          {invite.isError && (
            <p role="alert" className="text-xs text-danger">
              {apiErrorMessage(invite.error, 'Could not invite the user.')}
            </p>
          )}

          <DialogFooter className="mt-1">
            <DialogClose asChild>
              <Button variant="ghost" disabled={invite.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button
              type="submit"
              variant="primary"
              disabled={invite.isPending || email.trim() === ''}
            >
              {invite.isPending ? 'Inviting…' : 'Invite user'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
