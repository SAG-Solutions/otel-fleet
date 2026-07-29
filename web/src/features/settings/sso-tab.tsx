import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, KeyRound, Pencil, Plus, Trash2, XCircle } from 'lucide-react'
import {
  deleteAuthProviderConfigMutation,
  listAuthProviderConfigsOptions,
  listAuthProviderConfigsQueryKey,
  testAuthProviderConfigMutation,
  updateAuthProviderConfigMutation,
} from '@/api/generated/@tanstack/react-query.gen'
import { apiErrorMessage } from '@/lib/api-error'
import { ProviderMark, providerTypeLabel } from '@/features/settings/provider-meta'
import { ProviderDialog } from '@/features/settings/provider-dialog'
import { toast } from '@/components/toaster'
import { ErrorState } from '@/components/error-state'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { AuthProviderConfig } from '@/api/generated'

interface TestResult {
  ok: boolean
  message: string
}

export function SsoTab() {
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<AuthProviderConfig | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AuthProviderConfig | null>(null)
  const [testResults, setTestResults] = useState<Record<string, TestResult>>({})
  const [testingId, setTestingId] = useState<string | null>(null)

  const providersQuery = useQuery(listAuthProviderConfigsOptions())

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: listAuthProviderConfigsQueryKey() })

  const toggle = useMutation({
    ...updateAuthProviderConfigMutation(),
    onSuccess: (updated) => {
      void invalidate()
      toast(`"${updated.displayName}" ${updated.enabled ? 'enabled' : 'disabled'}`)
    },
    onError: (error) => toast(apiErrorMessage(error, 'Could not update the provider'), 'danger'),
  })

  const remove = useMutation({
    ...deleteAuthProviderConfigMutation(),
    onSuccess: (_data, variables) => {
      void invalidate()
      setDeleteTarget(null)
      setTestResults((prev) => {
        const next = { ...prev }
        delete next[variables.path.providerId]
        return next
      })
      toast('Provider deleted')
    },
    onError: (error) => {
      setDeleteTarget(null)
      toast(apiErrorMessage(error, 'Could not delete the provider'), 'danger')
    },
  })

  const test = useMutation({
    ...testAuthProviderConfigMutation(),
    onMutate: (variables) => setTestingId(variables.path.providerId),
    onSuccess: (result, variables) =>
      setTestResults((prev) => ({ ...prev, [variables.path.providerId]: result })),
    onError: (error, variables) =>
      setTestResults((prev) => ({
        ...prev,
        [variables.path.providerId]: {
          ok: false,
          message: apiErrorMessage(error, 'Test request failed'),
        },
      })),
    onSettled: () => setTestingId(null),
  })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-[13px] font-semibold text-ink">SSO providers</h2>
          <p className="text-xs text-ink-2">
            Sign-in options on the login page. Providers from environment variables are managed in
            deployment config.
          </p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setAddOpen(true)}>
          <Plus aria-hidden />
          Add provider
        </Button>
      </div>

      {providersQuery.isPending && (
        <div className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-4">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}
      {providersQuery.isError && (
        <ErrorState
          title="Could not load SSO providers"
          onRetry={() => void providersQuery.refetch()}
        />
      )}
      {providersQuery.isSuccess &&
        (providersQuery.data.providers.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-line bg-surface px-6 py-10 text-center">
            <KeyRound className="size-5 text-ink-3" />
            <div className="text-sm font-semibold text-ink">No SSO providers configured</div>
            <p className="max-w-md text-[13px] text-ink-2">
              Dev login keeps working without any providers. Add one to let your team sign in
              through Google, Microsoft, GitHub, or any OIDC identity provider.
            </p>
            <Button variant="outline" size="sm" className="mt-1" onClick={() => setAddOpen(true)}>
              <Plus aria-hidden />
              Add provider
            </Button>
          </div>
        ) : (
          <ProvidersTable
            providers={providersQuery.data.providers}
            testResults={testResults}
            testingId={testingId}
            onToggle={(provider, enabled) =>
              toggle.mutate({ path: { providerId: provider.id }, body: { enabled } })
            }
            onTest={(provider) => test.mutate({ path: { providerId: provider.id } })}
            onEdit={setEditTarget}
            onDelete={setDeleteTarget}
          />
        ))}

      <ProviderDialog open={addOpen} onOpenChange={setAddOpen} provider={null} />
      <ProviderDialog
        open={editTarget !== null}
        onOpenChange={(open) => {
          if (!open) setEditTarget(null)
        }}
        provider={editTarget}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={`Delete ${deleteTarget?.displayName ?? 'this provider'}?`}
        description="Users can no longer sign in through this provider. Linked identities remain and accounts are untouched — you can re-add the provider with the same slug later."
        confirmLabel="Delete provider"
        destructive
        pending={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate({ path: { providerId: deleteTarget.id } })
        }}
      />
    </div>
  )
}

function ProvidersTable({
  providers,
  testResults,
  testingId,
  onToggle,
  onTest,
  onEdit,
  onDelete,
}: {
  providers: AuthProviderConfig[]
  testResults: Record<string, TestResult>
  testingId: string | null
  onToggle: (provider: AuthProviderConfig, enabled: boolean) => void
  onTest: (provider: AuthProviderConfig) => void
  onEdit: (provider: AuthProviderConfig) => void
  onDelete: (provider: AuthProviderConfig) => void
}) {
  return (
    <section className="rounded-lg border border-line bg-surface">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Provider</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Slug</TableHead>
            <TableHead>Client ID</TableHead>
            <TableHead>Enabled</TableHead>
            <TableHead>Source</TableHead>
            <TableHead className="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {providers.map((provider) => {
            const fromEnv = provider.source === 'environment'
            const result = testResults[provider.id]
            return (
              <TableRow key={provider.id}>
                <TableCell>
                  <span className="flex items-center gap-2 font-medium text-ink">
                    <ProviderMark type={provider.type} />
                    {provider.displayName}
                  </span>
                </TableCell>
                <TableCell>
                  <Badge>{providerTypeLabel(provider.type)}</Badge>
                </TableCell>
                <TableCell>
                  <code className="font-mono text-xs text-ink-2">{provider.name}</code>
                </TableCell>
                <TableCell>
                  {/* SAML providers have no client id — show the IdP entity id, their key identifier. */}
                  {(() => {
                    const identifier =
                      provider.type === 'saml' ? (provider.idpEntityId ?? '') : provider.clientId
                    return identifier === '' ? (
                      <span className="text-xs text-ink-3">—</span>
                    ) : (
                      <code
                        className="block max-w-40 truncate font-mono text-xs text-ink-2"
                        title={identifier}
                      >
                        {identifier}
                      </code>
                    )
                  })()}
                </TableCell>
                <TableCell>
                  <span
                    title={
                      fromEnv ? 'Defined via OTEL_FLEET_OIDC_* environment variables' : undefined
                    }
                  >
                    <Switch
                      aria-label={`${provider.displayName} enabled`}
                      checked={provider.enabled}
                      disabled={fromEnv}
                      onCheckedChange={(enabled) => onToggle(provider, enabled)}
                    />
                  </span>
                </TableCell>
                <TableCell>
                  {fromEnv ? (
                    <Badge title="Read-only — defined via OTEL_FLEET_OIDC_* environment variables">
                      env
                    </Badge>
                  ) : (
                    <Badge variant="accent">database</Badge>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  {fromEnv ? (
                    <span className="text-xs text-ink-3">—</span>
                  ) : (
                    <div className="flex flex-col items-end gap-1">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={testingId === provider.id}
                          onClick={() => onTest(provider)}
                        >
                          {testingId === provider.id ? 'Testing…' : 'Test'}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          aria-label={`Edit ${provider.displayName}`}
                          onClick={() => onEdit(provider)}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 hover:text-danger"
                          aria-label={`Delete ${provider.displayName}`}
                          onClick={() => onDelete(provider)}
                        >
                          <Trash2 />
                        </Button>
                      </div>
                      {result && (
                        <span
                          role="status"
                          className={`inline-flex max-w-72 items-center gap-1 text-right text-[11px] ${
                            result.ok ? 'text-ok' : 'text-danger'
                          }`}
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
