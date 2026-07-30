import { useMemo } from 'react'
import { ChevronDown, ChevronUp, Plus, Trash2 } from 'lucide-react'
import { useDraftStore } from '@/features/pipelines/draft-store'
import { nodeFromCatalog, type NodeSection } from '@/features/pipelines/graph'
import { anchorDomId } from '@/features/pipelines/error-path'
import { BackendIcon, hasBackendIcon } from '@/features/pipelines/backend-icons'
import { ExporterGallery } from '@/features/pipelines/exporter-gallery'
import { SchemaForm, type JsonSchema } from '@/features/pipelines/schema-form'
import { SIGNAL_COLOR, SIGNAL_LABEL, SIGNALS } from '@/lib/chart-theme'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { CatalogComponent, CatalogPreset, GraphNode } from '@/api/generated'

export interface Catalog {
  processors: CatalogComponent[]
  exporters: CatalogComponent[]
  presets: CatalogPreset[]
}

/**
 * Left pane of the editor: structured builder over the draft graph —
 * signals, the ordered processor chain, and exporters, each card rendering
 * a schema-driven config form from the component catalog.
 */
export function PipelineBuilder({ catalog, readOnly }: { catalog: Catalog; readOnly: boolean }) {
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <SignalsCard readOnly={readOnly} />
      <NodeSectionCard
        section="processors"
        title="Processors"
        subtitle="Ordered chain — data flows top to bottom."
        components={catalog.processors}
        readOnly={readOnly}
      />
      <NodeSectionCard
        section="exporters"
        title="Exporters"
        subtitle="At least one destination is required."
        components={catalog.exporters}
        presets={catalog.presets}
        readOnly={readOnly}
      />
    </div>
  )
}

/** Signal stream checkboxes — shared between the form and graph views. */
export function SignalsCard({ readOnly }: { readOnly: boolean }) {
  const { theme } = useTheme()
  const signals = useDraftStore((s) => s.graph.signals)
  const toggleSignal = useDraftStore((s) => s.toggleSignal)

  return (
    <section
      id={anchorDomId({ section: 'signals', index: null })}
      className="rounded-lg border border-line bg-surface p-4 transition-shadow"
    >
      <h3 className="text-[13px] font-semibold text-ink">Signals</h3>
      <p className="mt-0.5 text-xs text-ink-2">
        Which of the customer&apos;s signal streams this pipeline consumes.
      </p>
      <div role="group" aria-label="Signals" className="mt-3 flex flex-wrap gap-2">
        {SIGNALS.map((signal) => {
          const checked = signals.includes(signal)
          return (
            <button
              key={signal}
              type="button"
              role="checkbox"
              aria-checked={checked}
              disabled={readOnly}
              onClick={() => toggleSignal(signal)}
              className={cn(
                'inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 text-[13px] transition-colors outline-none focus-visible:ring-2 focus-visible:ring-accent/70 disabled:cursor-not-allowed disabled:opacity-60',
                checked
                  ? 'border-line bg-surface-2 font-medium text-ink'
                  : 'border-line text-ink-3 hover:text-ink-2',
              )}
            >
              <span
                aria-hidden
                className={cn('size-2 rounded-full transition-opacity', !checked && 'opacity-25')}
                style={{ background: SIGNAL_COLOR[signal][theme] }}
              />
              {SIGNAL_LABEL[signal]}
            </button>
          )
        })}
      </div>
      {signals.length === 0 && (
        <p role="alert" className="mt-2 text-xs text-danger">
          Select at least one signal.
        </p>
      )}
    </section>
  )
}

/**
 * "+ processor / + exporter" catalog menu over the draft store — shared
 * between the form and graph views.
 */
export function AddNodeMenu({
  section,
  components,
  onAdded,
}: {
  section: NodeSection
  components: CatalogComponent[]
  /** Called with the index of the freshly added node. */
  onAdded?: (index: number) => void
}) {
  const addNode = useDraftStore((s) => s.addNode)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm">
          <Plus aria-hidden />
          Add {section === 'processors' ? 'processor' : 'exporter'}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Component catalog</DropdownMenuLabel>
        {components.length === 0 && (
          <div className="px-2 py-1.5 text-xs text-ink-3">Catalog unavailable.</div>
        )}
        {components.map((component) => (
          <DropdownMenuItem
            key={component.type}
            onSelect={() => {
              const index = useDraftStore.getState().graph[section].length
              addNode(section, nodeFromCatalog(component))
              onAdded?.(index)
            }}
            className="items-start"
          >
            <div className="min-w-0">
              <div className="flex items-baseline gap-2">
                <span className="font-medium text-ink">{component.displayName}</span>
                <span className="font-mono text-[11px] text-ink-3">{component.type}</span>
              </div>
              <div className="mt-0.5 line-clamp-2 text-xs text-ink-2">
                {component.description}
              </div>
            </div>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function NodeSectionCard({
  section,
  title,
  subtitle,
  components,
  presets,
  readOnly,
}: {
  section: NodeSection
  title: string
  subtitle: string
  components: CatalogComponent[]
  presets?: CatalogPreset[]
  readOnly: boolean
}) {
  const nodes = useDraftStore((s) => s.graph[section])
  const byType = useMemo(() => new Map(components.map((c) => [c.type, c])), [components])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h3 className="text-[13px] font-semibold text-ink">{title}</h3>
          <p className="text-xs text-ink-2">{subtitle}</p>
        </div>
        {!readOnly &&
          (section === 'exporters' ? (
            <ExporterGallery presets={presets ?? []} exporters={components} />
          ) : (
            <AddNodeMenu section={section} components={components} />
          ))}
      </div>

      {nodes.length === 0 ? (
        <div className="rounded-lg border border-dashed border-line bg-surface px-4 py-6 text-center text-xs text-ink-3">
          {section === 'processors'
            ? 'No processors — data passes through unchanged.'
            : 'No exporters — add at least one destination.'}
        </div>
      ) : (
        nodes.map((node, index) => (
          <NodeCard
            key={`${section}-${index}`}
            section={section}
            index={index}
            node={node}
            count={nodes.length}
            component={byType.get(node.type)}
            readOnly={readOnly}
          />
        ))
      )}
    </section>
  )
}

function NodeCard({
  section,
  index,
  node,
  count,
  component,
  readOnly,
}: {
  section: NodeSection
  index: number
  node: GraphNode
  count: number
  component: CatalogComponent | undefined
  readOnly: boolean
}) {
  const revision = useDraftStore((s) => s.revision)
  const moveNode = useDraftStore((s) => s.moveNode)
  const removeNode = useDraftStore((s) => s.removeNode)
  const setNodeName = useDraftStore((s) => s.setNodeName)
  const setNodeConfig = useDraftStore((s) => s.setNodeConfig)

  // Exporters: keep at least one (the graph contract requires minItems 1).
  const removable = !(section === 'exporters' && count <= 1)
  const label = component?.displayName ?? node.type

  return (
    <article
      id={anchorDomId({ section, index })}
      className="rounded-lg border border-line bg-surface transition-shadow"
    >
      <header className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
        {section === 'processors' && (
          <span className="font-mono text-[11px] text-ink-3 tabular-nums">{index + 1}.</span>
        )}
        {hasBackendIcon(component?.icon) && (
          <BackendIcon name={component?.icon} className="size-4 text-ink-2" />
        )}
        <span className="text-[13px] font-semibold text-ink">{label}</span>
        <code className="font-mono text-[11px] text-ink-3">{node.type}</code>
        <div className="ml-auto flex items-center gap-1">
          <Input
            aria-label={`Instance name for ${label}`}
            placeholder="instance name"
            className="h-6.5 w-32 font-mono text-[11px]"
            value={node.name ?? ''}
            disabled={readOnly}
            spellCheck={false}
            onChange={(e) => setNodeName(section, index, e.target.value)}
          />
          {!readOnly && section === 'processors' && (
            <>
              <Button
                variant="ghost"
                size="icon"
                className="h-6.5 w-6.5"
                aria-label={`Move ${label} up`}
                disabled={index === 0}
                onClick={() => moveNode(section, index, -1)}
              >
                <ChevronUp />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="h-6.5 w-6.5"
                aria-label={`Move ${label} down`}
                disabled={index === count - 1}
                onClick={() => moveNode(section, index, 1)}
              >
                <ChevronDown />
              </Button>
            </>
          )}
          {!readOnly && (
            <Button
              variant="ghost"
              size="icon"
              className="h-6.5 w-6.5 hover:text-danger"
              aria-label={`Remove ${label}`}
              disabled={!removable}
              title={removable ? undefined : 'A pipeline needs at least one exporter'}
              onClick={() => removeNode(section, index)}
            >
              <Trash2 />
            </Button>
          )}
        </div>
      </header>
      <div className="p-4">
        {component && fieldlessSchema(component.schema as JsonSchema) ? (
          <p className="text-xs text-ink-3">This component has no configuration.</p>
        ) : (
          <SchemaForm
            key={`${revision}-${section}-${index}-${node.type}`}
            schema={(component?.schema ?? {}) as JsonSchema}
            value={node.config}
            onChange={(config) => setNodeConfig(section, index, config)}
            readOnly={readOnly}
            idPrefix={`${section}-${index}`}
          />
        )}
      </div>
    </article>
  )
}

/** True when a component's schema declares no editable fields at all. */
export function fieldlessSchema(schema: JsonSchema): boolean {
  return (
    schema.type === 'object' &&
    Object.keys(schema.properties ?? {}).length === 0 &&
    !schema.additionalProperties
  )
}
