import { lazy, Suspense, useEffect, useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, History, ListTree, Rocket, Save, Trash2, Workflow } from 'lucide-react'
import {
  activatePipelineVersionMutation,
  createPipelineVersionMutation,
  deletePipelineMutation,
  getComponentCatalogOptions,
  getPipelineOptions,
  getPipelineQueryKey,
  getPipelineVersionOptions,
  listPipelinesQueryKey,
} from '@/api/generated/@tanstack/react-query.gen'
import { isTimeRange, DEFAULT_TIME_RANGE, type TimeRange } from '@/lib/time-range'
import { useMe, canMutate } from '@/hooks/use-me'
import { cn } from '@/lib/utils'
import { useDraftStore } from '@/features/pipelines/draft-store'
import { defaultGraph } from '@/features/pipelines/graph'
import { PipelineBuilder } from '@/features/pipelines/builder'
import { PreviewPanel } from '@/features/pipelines/preview-panel'
import { VersionsSheet } from '@/features/pipelines/versions-sheet'
import { StageMetricsTab } from '@/features/pipelines/stage-metrics'
import { ActiveVersionBadge, TargetClassBadge } from '@/features/pipelines/pipelines-table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorState } from '@/components/error-state'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { toast } from '@/components/toaster'
import type { PipelineDetail, RolloutStatus, ValidationResult } from '@/api/generated'

const TABS = ['builder', 'metrics'] as const
type Tab = (typeof TABS)[number]

const EDITOR_VIEWS = ['form', 'graph'] as const
type EditorView = (typeof EDITOR_VIEWS)[number]

interface PipelineSearch {
  tab?: Tab
  view?: EditorView
  range?: TimeRange
}

// Kept in a lazy chunk so @xyflow/react only loads when the graph view opens.
const PipelineGraphView = lazy(() => import('@/features/pipelines/flow-view'))

export const Route = createFileRoute('/_auth/pipelines/$pipelineId')({
  validateSearch: (search: Record<string, unknown>): PipelineSearch => ({
    tab: search.tab === 'metrics' ? 'metrics' : search.tab === 'builder' ? 'builder' : undefined,
    view: EDITOR_VIEWS.includes(search.view as EditorView)
      ? (search.view as EditorView)
      : undefined,
    range: isTimeRange(search.range) ? search.range : undefined,
  }),
  component: PipelinePage,
})

function PipelinePage() {
  const { pipelineId } = Route.useParams()
  const { tab = 'builder', range = DEFAULT_TIME_RANGE } = Route.useSearch()
  const navigate = Route.useNavigate()

  const pipelineQuery = useQuery(getPipelineOptions({ path: { pipelineId } }))

  if (pipelineQuery.isPending) return <PipelineSkeleton />
  if (pipelineQuery.isError) {
    return (
      <ErrorState
        title="Could not load this pipeline"
        detail="It may have been deleted, or the request failed."
        onRetry={() => void pipelineQuery.refetch()}
      />
    )
  }

  const pipeline = pipelineQuery.data

  return (
    <div className="flex flex-col gap-5">
      <PipelineHeader pipeline={pipeline} />
      <TabBar pipelineId={pipelineId} active={tab} />
      {tab === 'builder' ? (
        <EditorTab pipeline={pipeline} />
      ) : (
        <StageMetricsTab
          pipelineId={pipelineId}
          range={range}
          onRangeChange={(next) =>
            void navigate({ search: (prev) => ({ ...prev, range: next }), replace: true })
          }
        />
      )}
    </div>
  )
}

function PipelineHeader({ pipeline }: { pipeline: PipelineDetail }) {
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [confirmDelete, setConfirmDelete] = useState(false)

  const remove = useMutation({
    ...deletePipelineMutation(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: listPipelinesQueryKey() })
      toast(`Pipeline "${pipeline.name}" deleted`)
      void navigate({ to: '/pipelines' })
    },
    onError: () => toast('Could not delete the pipeline', 'danger'),
  })

  return (
    <div className="flex flex-col gap-3">
      <Link
        to="/pipelines"
        className="inline-flex w-fit items-center gap-1 rounded text-xs text-ink-3 outline-none hover:text-ink focus-visible:ring-2 focus-visible:ring-accent/70"
      >
        <ArrowLeft className="size-3" />
        Pipelines
      </Link>
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold text-ink">{pipeline.name}</h1>
        <ActiveVersionBadge pipeline={pipeline} />
        <TargetClassBadge targetClass={pipeline.targetClass} />
        {canMutate(me) && (
          <Button
            variant="danger"
            size="sm"
            className="ml-auto"
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2 aria-hidden />
            Delete
          </Button>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-ink-2">
        <span>
          <span className="text-ink-3">customer</span>{' '}
          <Link
            to="/customers/$customerId"
            params={{ customerId: pipeline.customerId }}
            className="rounded outline-none hover:text-accent hover:underline focus-visible:ring-2 focus-visible:ring-accent/70"
          >
            {pipeline.customerName ?? pipeline.customerId}
          </Link>
        </span>
        <span>
          <span className="text-ink-3">latest</span>{' '}
          <code className="font-mono">
            {pipeline.latestVersion != null ? `v${pipeline.latestVersion}` : '—'}
          </code>
        </span>
      </div>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete ${pipeline.name}?`}
        description="The pipeline and all its versions are removed. It disappears from the forwarding config on the next rollout."
        confirmLabel="Delete pipeline"
        destructive
        pending={remove.isPending}
        onConfirm={() => remove.mutate({ path: { pipelineId: pipeline.id } })}
      />
    </div>
  )
}

function TabBar({ pipelineId, active }: { pipelineId: string; active: Tab }) {
  const labels: Record<Tab, string> = { builder: 'Builder', metrics: 'Stage metrics' }
  return (
    <nav aria-label="Pipeline sections" className="flex gap-1 border-b border-line">
      {TABS.map((tab) => (
        <Link
          key={tab}
          to="/pipelines/$pipelineId"
          params={{ pipelineId }}
          search={(prev) => ({ ...prev, tab })}
          aria-current={active === tab ? 'page' : undefined}
          className={cn(
            '-mb-px rounded-t px-3 py-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-accent/70',
            active === tab
              ? 'border-b-2 border-accent font-medium text-ink'
              : 'border-b-2 border-transparent text-ink-2 hover:text-ink',
          )}
        >
          {labels[tab]}
        </Link>
      ))}
    </nav>
  )
}

/** Form | Graph segmented toggle for the editor — value lives in the URL. */
function ViewToggle({ view, onChange }: { view: EditorView; onChange: (next: EditorView) => void }) {
  const options: { value: EditorView; label: string; icon: typeof ListTree }[] = [
    { value: 'form', label: 'Form', icon: ListTree },
    { value: 'graph', label: 'Graph', icon: Workflow },
  ]
  return (
    <div
      role="radiogroup"
      aria-label="Editor view"
      className="inline-flex h-8 items-center gap-0.5 rounded-md border border-line bg-surface p-0.5"
    >
      {options.map(({ value, label, icon: Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={view === value}
          onClick={() => onChange(value)}
          className={cn(
            'inline-flex h-6.5 cursor-pointer items-center gap-1.5 rounded px-2.5 text-xs transition-colors outline-none focus-visible:ring-2 focus-visible:ring-accent/70',
            view === value ? 'bg-surface-2 font-semibold text-ink' : 'text-ink-3 hover:text-ink-2',
          )}
        >
          <Icon aria-hidden className="size-3.5" />
          {label}
        </button>
      ))}
    </div>
  )
}

function EditorTab({ pipeline }: { pipeline: PipelineDetail }) {
  const me = useMe()
  const canEdit = canMutate(me)
  const queryClient = useQueryClient()
  const { view = 'form' } = Route.useSearch()
  const navigate = Route.useNavigate()

  const catalogQuery = useQuery(getComponentCatalogOptions())

  // ---- draft seeding: latest version's graph, once per pipeline ----------
  const seed = useDraftStore((s) => s.seed)
  const seededPipelineId = useDraftStore((s) => s.pipelineId)
  const dirty = useDraftStore((s) => s.dirty)
  const needsSeed = seededPipelineId !== pipeline.id
  const latestVersion = pipeline.latestVersion ?? null

  const seedVersionQuery = useQuery({
    ...getPipelineVersionOptions({
      path: { pipelineId: pipeline.id, version: latestVersion ?? 0 },
    }),
    enabled: needsSeed && latestVersion !== null,
  })

  const catalog = catalogQuery.data
  const seedData = seedVersionQuery.data
  useEffect(() => {
    if (!needsSeed) return
    if (latestVersion === null) {
      seed(pipeline.id, defaultGraph(catalog), null)
    } else if (seedData) {
      seed(pipeline.id, seedData.graph, seedData.version)
    }
  }, [needsSeed, latestVersion, seedData, catalog, pipeline.id, seed])

  // ---- editor UI state ----------------------------------------------------
  const [versionsOpen, setVersionsOpen] = useState(false)
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null)
  const [confirmActivate, setConfirmActivate] = useState<number | null>(null)
  const [rollout, setRollout] = useState<RolloutStatus | null>(null)
  const [saveErrors, setSaveErrors] = useState<ValidationResult['errors'] | null>(null)

  const graph = useDraftStore((s) => s.graph)
  const markSaved = useDraftStore((s) => s.markSaved)

  // A rejected save's errors go stale as soon as the draft is edited again.
  useEffect(() => {
    setSaveErrors(null)
  }, [graph])

  const save = useMutation({
    ...createPipelineVersionMutation(),
    onSuccess: (version) => {
      setSaveErrors(null)
      markSaved(version.version)
      toast(`Saved as v${version.version}`)
      void queryClient.invalidateQueries({
        queryKey: getPipelineQueryKey({ path: { pipelineId: pipeline.id } }),
      })
      void queryClient.invalidateQueries({ queryKey: listPipelinesQueryKey() })
    },
    onError: (error) => {
      if (error && typeof error === 'object' && 'errors' in error && Array.isArray(error.errors)) {
        setSaveErrors((error as ValidationResult).errors)
      } else {
        toast('Could not save the version', 'danger')
      }
    },
  })

  const activate = useMutation({
    ...activatePipelineVersionMutation(),
    onSuccess: (status, variables) => {
      setRollout(status)
      setConfirmActivate(null)
      // Edge rollouts apply immediately over OpAMP — surface the push result.
      if (status.state === 'applied' && status.detail) {
        toast(status.detail)
      } else {
        toast(`v${variables.path.version} is now the active version`)
      }
      void queryClient.invalidateQueries({
        queryKey: getPipelineQueryKey({ path: { pipelineId: pipeline.id } }),
      })
      void queryClient.invalidateQueries({ queryKey: listPipelinesQueryKey() })
    },
    onError: () => toast('Activation failed — the version may be invalid', 'danger'),
  })

  // Newest-first list: first valid entry is the activation candidate.
  const latestValid = pipeline.versions.find((v) => v.validationStatus === 'valid')
  const nextVersion = (pipeline.latestVersion ?? 0) + 1
  const seeding = needsSeed

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        {canEdit && (
          <>
            <Button
              variant="primary"
              size="sm"
              disabled={seeding || save.isPending || graph.signals.length === 0}
              onClick={() => save.mutate({ path: { pipelineId: pipeline.id }, body: { graph } })}
            >
              <Save aria-hidden />
              {save.isPending ? 'Saving…' : `Save as v${nextVersion}`}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!latestValid || latestValid.active || activate.isPending}
              title={
                latestValid
                  ? latestValid.active
                    ? `v${latestValid.version} is already active`
                    : undefined
                  : 'No valid version to activate yet'
              }
              onClick={() => latestValid && setConfirmActivate(latestValid.version)}
            >
              <Rocket aria-hidden />
              {latestValid && !latestValid.active
                ? `Activate v${latestValid.version}`
                : 'Activate'}
            </Button>
          </>
        )}
        {dirty && <Badge variant="warn">unsaved changes</Badge>}
        <div className="ml-auto flex items-center gap-2">
          <ViewToggle
            view={view}
            onChange={(next) =>
              void navigate({ search: (prev) => ({ ...prev, view: next }), replace: true })
            }
          />
          <Button variant="ghost" size="sm" onClick={() => setVersionsOpen(true)}>
            <History aria-hidden />
            Versions ({pipeline.versions.length})
          </Button>
        </div>
      </div>

      {pipeline.targetClass === 'forwarding' && rollout?.state === 'pending_restart' && (
        <PendingRestartBanner rollout={rollout} />
      )}

      {catalogQuery.isError && (
        <div
          role="alert"
          className="rounded-md border border-warn/40 bg-warn/10 px-3 py-2.5 text-xs text-ink-2"
        >
          The component catalog could not be loaded — add menus are empty and configs fall back
          to raw JSON editing.
        </div>
      )}

      {view === 'form' ? (
        <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_minmax(0,30rem)]">
          <div className="min-w-0">
            {seeding || catalogQuery.isPending ? (
              <div className="flex flex-col gap-4">
                <Skeleton className="h-28 w-full" />
                <Skeleton className="h-40 w-full" />
                <Skeleton className="h-40 w-full" />
              </div>
            ) : (
              <PipelineBuilder
                catalog={catalog ?? { processors: [], exporters: [], presets: [] }}
                readOnly={!canEdit}
              />
            )}
          </div>

          <div className="min-w-0 xl:sticky xl:top-6">
            {seeding ? (
              <Skeleton className="h-64 w-full" />
            ) : (
              <PreviewPanel
                pipelineId={pipeline.id}
                targetClass={pipeline.targetClass}
                selectedVersion={selectedVersion}
                onClearSelection={() => setSelectedVersion(null)}
                onActivateVersion={(version) => setConfirmActivate(version)}
                canEdit={canEdit}
                saveErrors={saveErrors}
              />
            )}
          </div>
        </div>
      ) : (
        // Graph view: canvas on top, the validation/YAML preview below it —
        // both render the same draft store, so switching views is lossless.
        <div className="flex flex-col gap-5">
          {seeding || catalogQuery.isPending ? (
            <Skeleton className="h-[32rem] w-full" />
          ) : (
            <Suspense fallback={<Skeleton className="h-[32rem] w-full" />}>
              <PipelineGraphView
                catalog={catalog ?? { processors: [], exporters: [], presets: [] }}
                readOnly={!canEdit}
                targetClass={pipeline.targetClass}
                saveErrors={saveErrors}
              />
            </Suspense>
          )}
          <div className="min-w-0">
            {seeding ? (
              <Skeleton className="h-64 w-full" />
            ) : (
              <PreviewPanel
                pipelineId={pipeline.id}
                targetClass={pipeline.targetClass}
                selectedVersion={selectedVersion}
                onClearSelection={() => setSelectedVersion(null)}
                onActivateVersion={(version) => setConfirmActivate(version)}
                canEdit={canEdit}
                saveErrors={saveErrors}
              />
            )}
          </div>
        </div>
      )}

      <VersionsSheet
        open={versionsOpen}
        onOpenChange={setVersionsOpen}
        versions={pipeline.versions}
        selectedVersion={selectedVersion}
        onSelect={setSelectedVersion}
      />

      <ConfirmDialog
        open={confirmActivate !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmActivate(null)
        }}
        title={`Activate v${confirmActivate ?? ''}?`}
        description={
          pipeline.targetClass === 'edge'
            ? "The rendered configuration is pushed to this customer's connected edge agents over OpAMP immediately."
            : "The forwarding tier's configuration is regenerated with this version. In the compose dev setup the forwarding collector must be restarted to pick it up."
        }
        confirmLabel="Activate version"
        pending={activate.isPending}
        onConfirm={() => {
          if (confirmActivate !== null) {
            activate.mutate({ path: { pipelineId: pipeline.id, version: confirmActivate } })
          }
        }}
      />
    </div>
  )
}

/** Compose-dev rollouts need a manual collector restart — keep it visible. */
function PendingRestartBanner({ rollout }: { rollout: RolloutStatus }) {
  return (
    <div
      role="status"
      className="flex flex-col gap-2 rounded-lg border border-accent/40 bg-accent/10 px-4 py-3"
    >
      <div className="text-[13px] font-medium text-ink">
        v{rollout.activeVersion} is published, but the forwarding collector has not picked it up
        yet.
      </div>
      {rollout.detail && (
        <div className="flex items-start gap-1 rounded-md border border-line bg-surface-2 p-2.5">
          <code className="min-w-0 flex-1 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink-2">
            {rollout.detail}
          </code>
          <CopyButton value={rollout.detail} label="Copy restart hint" />
        </div>
      )}
    </div>
  )
}

function PipelineSkeleton() {
  return (
    <div className="flex flex-col gap-5">
      <Skeleton className="h-4 w-24" />
      <Skeleton className="h-8 w-72" />
      <Skeleton className="h-4 w-96" />
      <Skeleton className="h-9 w-full" />
      <div className="grid items-start gap-5 xl:grid-cols-2">
        <Skeleton className="h-72 w-full" />
        <Skeleton className="h-72 w-full" />
      </div>
    </div>
  )
}
