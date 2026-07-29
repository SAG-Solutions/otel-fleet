import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, KeyRound, Plus, Terminal } from 'lucide-react'
import {
  createApiTokenMutation,
  listApiTokensOptions,
  listApiTokensQueryKey,
  revokeApiTokenMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import { apiErrorMessage } from '@/lib/api-error'
import { formatRelative } from '@/lib/format'
import { toast } from '@/components/toaster'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import type { ApiToken, ApiTokenCreated, Role } from '@/api/generated'

const ROLE_DESCRIPTION: Record<Role, string> = {
  admin: 'Everything, plus users, SSO settings, and the audit log',
  operator: 'Manage customers, pipelines, and the fleet',
  viewer: 'Read-only access to the whole console',
}

const ROLES: readonly Role[] = ['admin', 'operator', 'viewer'] as const

export function ApiTokensTab() {
  const queryClient = useQueryClient()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [created, setCreated] = useState<ApiTokenCreated | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<ApiToken | null>(null)

  const query = useQuery(listApiTokensOptions())
  const invalidate = () => queryClient.invalidateQueries({ queryKey: listApiTokensQueryKey() })

  const revoke = useMutation({
    ...revokeApiTokenMutation(),
    onSuccess: () => {
      void invalidate()
      setRevokeTarget(null)
      toast('Token revoked')
    },
    onError: (error) => {
      setRevokeTarget(null)
      toast(apiErrorMessage(error, 'Could not revoke the token'), 'danger')
    },
  })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">API tokens</h2>
          <p className="text-xs text-ink-2">
            Personal access tokens for the <code className="mx-0.5 font-mono">otel-fleetctl</code>{' '}
            CLI, CI pipelines, and automation. Each token authenticates as an
            <code className="mx-1 font-mono">Authorization: Bearer</code>
            header with a fixed role.
          </p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setDialogOpen(true)}>
          <Plus aria-hidden />
          Create token
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
        <ErrorState title="Could not load API tokens" onRetry={() => void query.refetch()} />
      )}
      {query.isSuccess &&
        (query.data.tokens.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
            <Terminal className="size-5 text-ink-3" />
            <div className="text-sm font-semibold text-ink">No API tokens</div>
            <p className="max-w-md text-[13px] text-ink-2">
              Create a token for programmatic access — the{' '}
              <code className="font-mono">otel-fleetctl</code> CLI, CI jobs, or scripts that call the
              otel-fleet API. Tokens are admin-managed and carry a fixed role.
            </p>
            <Button variant="outline" size="sm" className="mt-1" onClick={() => setDialogOpen(true)}>
              <Plus aria-hidden />
              Create token
            </Button>
          </div>
        ) : (
          <section className="rounded-lg border border-line bg-surface">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Name</TableHead>
                  <TableHead>Prefix</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.data.tokens.map((token) => {
                  const revoked = Boolean(token.revokedAt)
                  return (
                    <TableRow key={token.id}>
                      <TableCell className="font-medium text-ink">{token.name}</TableCell>
                      <TableCell>
                        <code className="font-mono text-xs text-ink-2">{token.tokenPrefix}</code>
                      </TableCell>
                      <TableCell>
                        <Badge variant={token.role === 'admin' ? 'accent' : 'neutral'}>
                          {token.role}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-xs text-ink-2">
                        <span title={new Date(token.createdAt).toISOString()}>
                          {formatRelative(token.createdAt)}
                        </span>
                      </TableCell>
                      <TableCell className="text-xs text-ink-2">
                        {token.expiresAt ? (
                          <span title={new Date(token.expiresAt).toISOString()}>
                            {formatRelative(token.expiresAt)}
                          </span>
                        ) : (
                          <span className="text-ink-3">never</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-ink-2">
                        {token.lastUsedAt ? (
                          <span title={new Date(token.lastUsedAt).toISOString()}>
                            {formatRelative(token.lastUsedAt)}
                          </span>
                        ) : (
                          <span className="text-ink-3">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {revoked ? (
                          <Badge variant="neutral">Revoked</Badge>
                        ) : (
                          <Badge dot variant="ok">
                            Active
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        {revoked ? (
                          <span className="text-xs text-ink-3">—</span>
                        ) : (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="hover:text-danger"
                            onClick={() => setRevokeTarget(token)}
                          >
                            Revoke
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </section>
        ))}

      <CreateTokenDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onCreated={(token) => {
          void invalidate()
          setCreated(token)
        }}
      />
      <TokenSecretDialog token={created} onClose={() => setCreated(null)} />
      <ConfirmDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRevokeTarget(null)
        }}
        title={`Revoke ${revokeTarget?.name ?? 'this token'}?`}
        description="Any CLI session, CI job, or script using this token stops working immediately. This cannot be undone — issue a new token to restore access."
        confirmLabel="Revoke token"
        destructive
        pending={revoke.isPending}
        onConfirm={() => {
          if (revokeTarget) revoke.mutate({ path: { tokenId: revokeTarget.id } })
        }}
      />
    </div>
  )
}

function CreateTokenDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (token: ApiTokenCreated) => void
}) {
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>('operator')
  const [expiresAt, setExpiresAt] = useState('')

  // Reset the form each time the dialog opens.
  const [seeded, setSeeded] = useState(false)
  if (open && !seeded) {
    setName('')
    setRole('operator')
    setExpiresAt('')
    setSeeded(true)
  }
  if (!open && seeded) setSeeded(false)

  const create = useMutation({
    ...createApiTokenMutation(),
    onSuccess: (token) => {
      onOpenChange(false)
      onCreated(token)
    },
  })

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (name.trim() === '') return
    create.mutate({
      body: {
        name: name.trim(),
        role,
        ...(expiresAt !== '' ? { expiresAt: new Date(expiresAt).toISOString() } : {}),
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create API token</DialogTitle>
          <DialogDescription>
            The full token is shown exactly once after creation — only a hash is stored.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="token-name">Name</Label>
            <Input
              id="token-name"
              required
              maxLength={200}
              placeholder="ci-deploy"
              value={name}
              onChange={(e) => setName(e.target.value)}
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
                    name="token-role"
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

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="token-expiry">
              Expires <span className="font-normal text-ink-3">(optional)</span>
            </Label>
            <Input
              id="token-expiry"
              type="datetime-local"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
            <p className="text-xs text-ink-3">Leave empty for a non-expiring token.</p>
          </div>

          <p className="flex items-start gap-1.5 text-xs text-ink-3">
            <Info aria-hidden className="mt-px size-3.5 shrink-0" />
            The token acts with this role for anyone who holds it. Scope it down and set an expiry
            for CI.
          </p>

          {create.isError && (
            <p role="alert" className="text-xs text-danger">
              {apiErrorMessage(create.error, 'Could not create the token.')}
            </p>
          )}

          <DialogFooter className="mt-1">
            <DialogClose asChild>
              <Button variant="ghost" disabled={create.isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button
              type="submit"
              variant="primary"
              disabled={create.isPending || name.trim() === ''}
            >
              {create.isPending ? 'Creating…' : 'Create token'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Show-once token dialog. The secret is never retrievable again, so the dialog
 * closes only through the explicit acknowledgement button.
 */
function TokenSecretDialog({
  token,
  onClose,
}: {
  token: ApiTokenCreated | null
  onClose: () => void
}) {
  const origin = typeof window !== 'undefined' ? window.location.origin : 'https://otel-fleet.example.com'
  const usage = token
    ? [
        `export OTEL_FLEET_URL=${origin}`,
        `export OTEL_FLEET_TOKEN=${token.secret}`,
        `otel-fleetctl customers`,
      ].join('\n')
    : ''

  return (
    <Dialog
      open={token !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        hideClose
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        {token && (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <KeyRound className="size-4 text-accent" />
                Save your API token now
              </DialogTitle>
              <DialogDescription>
                This is the only time <span className="font-mono text-ink">{token.name}</span> is
                shown. otel-fleet stores a hash — the token cannot be recovered later.
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-1 rounded-md border border-warn/40 bg-surface-2 p-3">
              <code
                data-testid="api-token-secret"
                className="min-w-0 flex-1 font-mono text-xs break-all text-ink"
              >
                {token.secret}
              </code>
              <CopyButton value={token.secret} label="Copy API token" />
            </div>
            <p className="mt-1 text-xs text-ink-3">
              Send it as{' '}
              <code className="font-mono">Authorization: Bearer &lt;token&gt;</code> on API
              requests. Prefix <code className="font-mono">{token.tokenPrefix}</code> identifies it
              in this console.
            </p>
            <div className="mt-2 flex flex-col gap-1.5">
              <span className="text-xs font-medium text-ink-2">Use it with the CLI</span>
              <div className="flex items-start gap-1 rounded-md border border-line bg-surface-2 p-3">
                <code className="min-w-0 flex-1 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink-2">
                  {usage}
                </code>
                <CopyButton value={usage} label="Copy CLI snippet" />
              </div>
            </div>
            <DialogFooter>
              <Button variant="primary" onClick={onClose}>
                I saved the token
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
