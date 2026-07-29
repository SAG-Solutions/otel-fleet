import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { FolderSync, Trash2, UserPlus } from 'lucide-react'
import {
  deleteUserMutation,
  listCustomersOptions,
  listUsersOptions,
  listUsersQueryKey,
  updateUserMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import { apiErrorMessage } from '@/lib/api-error'
import { formatRelative } from '@/lib/format'
import { useMe } from '@/hooks/use-me'
import { InviteUserDialog } from '@/features/settings/invite-user-dialog'
import { EditAccessDialog } from '@/features/settings/edit-access-dialog'
import { toast } from '@/components/toaster'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
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
import type { Role, UserAccount } from '@/api/generated'

const ROLES: readonly Role[] = ['admin', 'operator', 'viewer'] as const

export function UsersTab() {
  const me = useMe()
  const queryClient = useQueryClient()
  const [inviteOpen, setInviteOpen] = useState(false)
  const [disableTarget, setDisableTarget] = useState<UserAccount | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<UserAccount | null>(null)
  const [accessTarget, setAccessTarget] = useState<UserAccount | null>(null)

  const usersQuery = useQuery(listUsersOptions())
  const customersQuery = useQuery(listCustomersOptions())
  const customerNames = new Map(
    (customersQuery.data?.customers ?? []).map((c) => [c.id, c.name]),
  )

  const invalidate = () => queryClient.invalidateQueries({ queryKey: listUsersQueryKey() })

  const update = useMutation({
    ...updateUserMutation(),
    onSuccess: (updated) => {
      void invalidate()
      setDisableTarget(null)
      toast(`${updated.email} updated`)
    },
    onError: (error) => {
      setDisableTarget(null)
      // 409s carry last-admin / self-demote protections — show the API reason.
      toast(apiErrorMessage(error, 'Could not update the user'), 'danger')
    },
  })

  const remove = useMutation({
    ...deleteUserMutation(),
    onSuccess: () => {
      void invalidate()
      setDeleteTarget(null)
      toast('User deleted')
    },
    onError: (error) => {
      setDeleteTarget(null)
      toast(apiErrorMessage(error, 'Could not delete the user'), 'danger')
    },
  })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">Users</h2>
          <p className="text-xs text-ink-2">
            Console accounts. Invites allow-list an email; the account activates on first SSO
            login.
          </p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setInviteOpen(true)}>
          <UserPlus aria-hidden />
          Invite user
        </Button>
      </div>

      {usersQuery.isPending && (
        <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}
      {usersQuery.isError && (
        <ErrorState title="Could not load users" onRetry={() => void usersQuery.refetch()} />
      )}
      {usersQuery.isSuccess && (
        <UsersTable
          users={usersQuery.data.users}
          selfId={me?.id}
          pending={update.isPending}
          customerName={(id) => customerNames.get(id)}
          onRoleChange={(user, role) =>
            update.mutate({ path: { userId: user.id }, body: { role } })
          }
          onEnable={(user) =>
            update.mutate({ path: { userId: user.id }, body: { disabled: false } })
          }
          onDisable={setDisableTarget}
          onDelete={setDeleteTarget}
          onEditAccess={setAccessTarget}
        />
      )}

      <ScimInfoCard />

      <InviteUserDialog open={inviteOpen} onOpenChange={setInviteOpen} />
      <EditAccessDialog user={accessTarget} onClose={() => setAccessTarget(null)} />
      <ConfirmDialog
        open={disableTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDisableTarget(null)
        }}
        title={`Disable ${disableTarget?.email ?? 'this user'}?`}
        description="The user is signed out and cannot log in until re-enabled. Nothing is deleted."
        confirmLabel="Disable user"
        destructive
        pending={update.isPending}
        onConfirm={() => {
          if (disableTarget) {
            update.mutate({ path: { userId: disableTarget.id }, body: { disabled: true } })
          }
        }}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={`Delete ${deleteTarget?.email ?? 'this user'}?`}
        description="Deletion is permanent and removes the account and its linked identities. The email can be re-invited later."
        confirmLabel="Delete user"
        destructive
        pending={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate({ path: { userId: deleteTarget.id } })
        }}
      />
    </div>
  )
}

function ScimInfoCard() {
  const origin =
    typeof window !== 'undefined' ? window.location.origin : 'https://otel-fleet.example.com'
  const baseUrl = `${origin}/scim/v2`

  return (
    <section className="flex flex-col gap-2 rounded-lg border border-line bg-surface-2 p-4">
      <div className="flex items-center gap-2">
        <FolderSync aria-hidden className="size-3.5 text-ink-3" />
        <h3 className="text-xs font-semibold text-ink-2">Directory provisioning (SCIM)</h3>
      </div>
      <div className="flex items-center gap-1">
        <code className="min-w-0 flex-1 truncate font-mono text-xs text-ink" title={baseUrl}>
          {baseUrl}
        </code>
        <CopyButton value={baseUrl} label="Copy SCIM base URL" />
      </div>
      <p className="text-xs text-ink-3">
        Point your identity provider (Okta, Entra ID, …) here and authenticate with an admin{' '}
        <Link
          to="/settings"
          search={{ tab: 'tokens' }}
          className="text-accent underline-offset-2 hover:underline"
        >
          API token
        </Link>
        . Provisioned users appear above with the viewer role by default; set their role and customer
        access here.
      </p>
    </section>
  )
}

const MAX_ACCESS_CHIPS = 3

function CustomerAccessCell({
  user,
  customerName,
}: {
  user: UserAccount
  customerName: (id: string) => string | undefined
}) {
  // Admins reach every customer regardless of any stored grants; a non-admin
  // with no grants is likewise unrestricted (backward compatible).
  const grants = user.customerIds ?? []
  if (user.role === 'admin' || grants.length === 0) {
    return (
      <Badge variant="neutral" className="text-ink-3">
        All customers
      </Badge>
    )
  }

  const shown = grants.slice(0, MAX_ACCESS_CHIPS)
  const overflow = grants.length - shown.length
  return (
    <span className="flex flex-wrap gap-1">
      {shown.map((id) => (
        <Badge key={id} variant="neutral">
          {customerName(id) ?? id}
        </Badge>
      ))}
      {overflow > 0 && <Badge variant="neutral">{`+${overflow}`}</Badge>}
    </span>
  )
}

function userStatus(user: UserAccount): { label: string; variant: 'ok' | 'warn' | 'neutral' } {
  if (user.disabled) return { label: 'Disabled', variant: 'neutral' }
  if (user.invited) return { label: 'Invited', variant: 'warn' }
  return { label: 'Active', variant: 'ok' }
}

function UsersTable({
  users,
  selfId,
  pending,
  customerName,
  onRoleChange,
  onEnable,
  onDisable,
  onDelete,
  onEditAccess,
}: {
  users: UserAccount[]
  selfId: string | undefined
  pending: boolean
  customerName: (id: string) => string | undefined
  onRoleChange: (user: UserAccount, role: Role) => void
  onEnable: (user: UserAccount) => void
  onDisable: (user: UserAccount) => void
  onDelete: (user: UserAccount) => void
  onEditAccess: (user: UserAccount) => void
}) {
  return (
    <section className="rounded-lg border border-line bg-surface">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Email</TableHead>
            <TableHead>Name</TableHead>
            <TableHead>Role</TableHead>
            <TableHead>Customer access</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Identities</TableHead>
            <TableHead>Last login</TableHead>
            <TableHead className="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {users.map((user) => {
            const isSelf = user.id === selfId
            const status = userStatus(user)
            return (
              <TableRow key={user.id}>
                <TableCell>
                  <code className="font-mono text-xs text-ink">{user.email}</code>
                  {isSelf && (
                    <Badge className="ml-2" variant="accent">
                      you
                    </Badge>
                  )}
                </TableCell>
                <TableCell className="text-[13px] text-ink-2">
                  {user.displayName ?? '—'}
                </TableCell>
                <TableCell>
                  <span
                    title={isSelf ? 'You cannot change your own role' : undefined}
                    className="inline-flex"
                  >
                    <Select
                      aria-label={`Role for ${user.email}`}
                      className="w-28"
                      value={user.role}
                      disabled={isSelf || pending}
                      onChange={(e) => onRoleChange(user, e.target.value as Role)}
                    >
                      {ROLES.map((role) => (
                        <option key={role} value={role}>
                          {role}
                        </option>
                      ))}
                    </Select>
                  </span>
                </TableCell>
                <TableCell>
                  <CustomerAccessCell user={user} customerName={customerName} />
                </TableCell>
                <TableCell>
                  <Badge dot variant={status.variant}>
                    {status.label}
                  </Badge>
                </TableCell>
                <TableCell>
                  {user.identities.length === 0 ? (
                    <span className="text-xs text-ink-3">—</span>
                  ) : (
                    <span className="flex flex-wrap gap-1">
                      {user.identities.map((identity) => (
                        <Badge key={identity} className="font-mono">
                          {identity}
                        </Badge>
                      ))}
                    </span>
                  )}
                </TableCell>
                <TableCell className="text-xs text-ink-2">
                  {user.lastLoginAt ? (
                    <span title={new Date(user.lastLoginAt).toISOString()}>
                      {formatRelative(user.lastLoginAt)}
                    </span>
                  ) : (
                    'Never'
                  )}
                </TableCell>
                <TableCell className="text-right">
                  {isSelf ? (
                    <span className="text-xs text-ink-3">—</span>
                  ) : (
                    <div className="flex items-center justify-end gap-1">
                      {user.role !== 'admin' && (
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Edit access for ${user.email}`}
                          onClick={() => onEditAccess(user)}
                        >
                          Edit access
                        </Button>
                      )}
                      {user.disabled ? (
                        <Button variant="ghost" size="sm" onClick={() => onEnable(user)}>
                          Enable
                        </Button>
                      ) : (
                        <Button variant="ghost" size="sm" onClick={() => onDisable(user)}>
                          Disable
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 hover:text-danger"
                        aria-label={`Delete ${user.email}`}
                        onClick={() => onDelete(user)}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </section>
  )
}
