import { useState, type FormEvent } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import { ArrowRight, ExternalLink } from 'lucide-react'
import {
  devLoginMutation,
  listAuthProvidersOptions,
} from '@/api/generated/@tanstack/react-query.gen'
import { Wordmark } from '@/components/wordmark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorState } from '@/components/error-state'

interface LoginSearch {
  redirect?: string
}

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  }),
  component: LoginPage,
})

function LoginPage() {
  const providersQuery = useQuery(listAuthProvidersOptions())

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg p-6">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <Wordmark large />
          <p className="text-[13px] text-ink-2">
            Fleet control for your OpenTelemetry ingest — sign in to operate.
          </p>
        </div>

        <div className="rounded-lg border border-line bg-surface p-5">
          {providersQuery.isPending && (
            <div className="flex flex-col gap-2">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {providersQuery.isError && (
            <ErrorState
              title="Could not load sign-in options"
              onRetry={() => void providersQuery.refetch()}
            />
          )}
          {providersQuery.isSuccess && (
            <ProviderList
              providers={providersQuery.data.providers}
              devLoginEnabled={providersQuery.data.devLoginEnabled}
            />
          )}
        </div>
      </div>
    </div>
  )
}

function ProviderList({
  providers,
  devLoginEnabled,
}: {
  providers: { name: string; displayName: string; loginUrl: string }[]
  devLoginEnabled: boolean
}) {
  if (providers.length === 0 && !devLoginEnabled) {
    return (
      <p className="text-center text-[13px] text-ink-2">
        No sign-in methods are configured. Set an OIDC provider or{' '}
        <code className="font-mono text-xs">OTEL_FLEET_DEV_LOGIN=true</code> on the server.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-2">
      {providers.map((provider) => (
        <Button
          key={provider.name}
          variant="outline"
          className="w-full justify-between"
          onClick={() => {
            window.location.href = provider.loginUrl
          }}
        >
          Continue with {provider.displayName}
          <ExternalLink aria-hidden />
        </Button>
      ))}
      {devLoginEnabled && (
        <>
          {providers.length > 0 && (
            <div className="my-2 flex items-center gap-3 text-[11px] tracking-wider text-ink-3 uppercase">
              <span className="h-px flex-1 bg-line" />
              local development
              <span className="h-px flex-1 bg-line" />
            </div>
          )}
          <DevLoginForm />
        </>
      )}
    </div>
  )
}

function DevLoginForm() {
  const [email, setEmail] = useState('')
  const navigate = useNavigate()
  const { redirect } = Route.useSearch()

  const login = useMutation({
    ...devLoginMutation(),
    onSuccess: () => {
      void navigate({ to: redirect ?? '/' })
    },
  })

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (email.trim() === '') return
    login.mutate({ body: { email: email.trim() } })
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <Label htmlFor="dev-email">Email</Label>
      <Input
        id="dev-email"
        type="email"
        required
        autoComplete="email"
        placeholder="you@example.com"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
      />
      <Button type="submit" variant="primary" className="w-full" disabled={login.isPending}>
        {login.isPending ? 'Signing in…' : 'Dev login'}
        <ArrowRight aria-hidden />
      </Button>
      {login.isError && (
        <p role="alert" className="text-xs text-danger">
          Dev login failed — it may be disabled on this server.
        </p>
      )}
    </form>
  )
}
