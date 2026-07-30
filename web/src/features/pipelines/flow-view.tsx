import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  applyNodeChanges,
  Controls,
  Handle,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
  type NodeTypes,
} from '@xyflow/react'
import { Send, SlidersHorizontal, Trash2, X } from 'lucide-react'
import '@xyflow/react/dist/style.css'
import './flow-view.css'
import { useDraftStore } from '@/features/pipelines/draft-store'
import {
  buildFlowLayout,
  configSummary,
  invalidNodeIds,
  isIdentityOrder,
  orderFromY,
  parseComponentNodeId,
} from '@/features/pipelines/flow-layout'
import type { NodeSection } from '@/features/pipelines/graph'
import {
  AddNodeMenu,
  fieldlessSchema,
  SignalsCard,
  type Catalog,
} from '@/features/pipelines/builder'
import { BackendIcon, hasBackendIcon } from '@/features/pipelines/backend-icons'
import { ExporterGallery } from '@/features/pipelines/exporter-gallery'
import { useDraftValidation } from '@/features/pipelines/preview-panel'
import { SchemaForm, type JsonSchema } from '@/features/pipelines/schema-form'
import { SIGNAL_COLOR, SIGNAL_LABEL } from '@/lib/chart-theme'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { Pipeline, Signal, ValidationResult } from '@/api/generated'

type TargetClass = Pipeline['targetClass']
type ValidationErrors = ValidationResult['errors']

interface Selection {
  section: NodeSection
  index: number
}

interface SourceNodeData extends Record<string, unknown> {
  signal: Signal
  invalid: boolean
}

interface ComponentNodeData extends Record<string, unknown> {
  section: NodeSection
  index: number
  label: string
  typeId: string
  icon: string | null
  name: string | null
  summary: string[]
  invalid: boolean
  active: boolean
}

type SourceFlowNode = Node<SourceNodeData, 'source'>
type ComponentFlowNode = Node<ComponentNodeData, 'component'>
type FlowNode = SourceFlowNode | ComponentFlowNode

/**
 * Graph view of the pipeline editor. Renders the same draft store as the
 * form view as a left-to-right flow: signal sources → processor chain →
 * exporter fan-out. Edges are derived from the chain order and cannot be
 * rewired or deleted; drag processors vertically to reorder the chain,
 * click a node to edit it in the side panel.
 */
export default function PipelineGraphView({
  catalog,
  readOnly,
  targetClass,
  saveErrors,
}: {
  catalog: Catalog
  readOnly: boolean
  targetClass: TargetClass
  saveErrors: ValidationErrors | null
}) {
  const graph = useDraftStore((s) => s.graph)
  const [selected, setSelected] = useState<Selection | null>(null)

  // Shares the preview panel's validation query (same structural key).
  const { query } = useDraftValidation(graph, targetClass)
  const validation = query.data
  const errors = useMemo<ValidationErrors>(
    () => [
      ...(saveErrors ?? []),
      ...(validation && !validation.valid ? validation.errors : []),
    ],
    [saveErrors, validation],
  )
  const invalidIds = useMemo(
    () => invalidNodeIds(errors, graph.signals),
    [errors, graph.signals],
  )

  // Drop the selection when its node disappears (remove, restore, re-seed).
  useEffect(() => {
    if (selected && selected.index >= graph[selected.section].length) setSelected(null)
  }, [graph, selected])

  return (
    <div className="flex flex-col gap-4">
      <SignalsCard readOnly={readOnly} />

      <div className="flex flex-wrap items-center gap-2">
        {!readOnly && (
          <>
            <AddNodeMenu
              section="processors"
              components={catalog.processors}
              onAdded={(index) => setSelected({ section: 'processors', index })}
            />
            <ExporterGallery
              presets={catalog.presets}
              exporters={catalog.exporters}
              onAdded={(index) => setSelected({ section: 'exporters', index })}
            />
          </>
        )}
        <p className="text-xs text-ink-3">
          Edges follow the chain order and can&apos;t be rewired yet — drag processors vertically
          to reorder, click a node to edit.
        </p>
      </div>

      <div
        className={cn(
          'grid items-start gap-4',
          selected !== null && 'xl:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]',
        )}
      >
        <ReactFlowProvider>
          <FlowCanvas
            catalog={catalog}
            invalidIds={invalidIds}
            selected={selected}
            onSelect={setSelected}
            readOnly={readOnly}
          />
        </ReactFlowProvider>
        {selected !== null && (
          <NodeEditorPanel
            key={`${selected.section}-${selected.index}`}
            selection={selected}
            catalog={catalog}
            readOnly={readOnly}
            onClose={() => setSelected(null)}
          />
        )}
      </div>
    </div>
  )
}

const nodeTypes: NodeTypes = {
  source: SourceNode,
  component: ComponentNode,
}

function FlowCanvas({
  catalog,
  invalidIds,
  selected,
  onSelect,
  readOnly,
}: {
  catalog: Catalog
  invalidIds: Set<string>
  selected: Selection | null
  onSelect: (next: Selection | null) => void
  readOnly: boolean
}) {
  const { theme } = useTheme()
  const graph = useDraftStore((s) => s.graph)
  const reorderNodes = useDraftStore((s) => s.reorderNodes)
  const { getNodes, fitView } = useReactFlow()

  const metaByType = useMemo(
    () =>
      new Map(
        [...catalog.processors, ...catalog.exporters].map((c) => [
          c.type,
          { label: c.displayName, icon: c.icon ?? null },
        ]),
      ),
    [catalog],
  )

  const layoutNodes = useMemo<FlowNode[]>(() => {
    const { nodes } = buildFlowLayout(graph)
    return nodes.map((spec) => {
      if (spec.kind === 'source') {
        return {
          id: spec.id,
          type: 'source' as const,
          position: spec.position,
          draggable: false,
          data: { signal: spec.signal!, invalid: invalidIds.has(spec.id) },
        }
      }
      const node = graph[spec.section!][spec.index!]!
      const meta = metaByType.get(node.type)
      return {
        id: spec.id,
        type: 'component' as const,
        position: spec.position,
        draggable: !readOnly && spec.section === 'processors',
        data: {
          section: spec.section!,
          index: spec.index!,
          label: meta?.label ?? node.type,
          typeId: node.type,
          icon: meta?.icon ?? null,
          name: node.name ?? null,
          summary: configSummary(node.config),
          invalid: invalidIds.has(spec.id),
          active: selected?.section === spec.section && selected.index === spec.index,
        },
      }
    })
  }, [graph, metaByType, invalidIds, selected, readOnly])

  const edges = useMemo<Edge[]>(
    () =>
      buildFlowLayout(graph).edges.map((edge) => ({
        ...edge,
        selectable: false,
        deletable: false,
        focusable: false,
      })),
    [graph],
  )

  // Controlled nodes: layout is the source of truth, drags are applied on
  // top and either committed (reorder) or snapped back on drop.
  const [nodes, setNodes] = useState<FlowNode[]>(layoutNodes)
  useEffect(() => setNodes(layoutNodes), [layoutNodes])

  const onNodesChange = useCallback(
    (changes: NodeChange<FlowNode>[]) =>
      setNodes((current) => applyNodeChanges(changes, current)),
    [],
  )

  const onNodeDragStop = useCallback(() => {
    const rows = getNodes()
      .filter(
        (node): node is ComponentFlowNode =>
          node.type === 'component' &&
          (node.data as ComponentNodeData).section === 'processors',
      )
      .map((node) => ({ index: (node.data as ComponentNodeData).index, y: node.position.y }))
    const order = orderFromY(rows)
    if (isIdentityOrder(order)) {
      // Dropped back into place — snap to the computed layout.
      setNodes(layoutNodes)
      return
    }
    reorderNodes('processors', order)
    // Keep the side panel on the node that was followed, not its old slot.
    if (selected?.section === 'processors') {
      onSelect({ section: 'processors', index: order.indexOf(selected.index) })
    }
  }, [getNodes, layoutNodes, reorderNodes, selected, onSelect])

  // fitView on mount, when the graph shape changes, and when the canvas
  // container resizes.
  const structure = `${graph.signals.length}/${graph.processors.length}/${graph.exporters.length}`
  useEffect(() => {
    const frame = requestAnimationFrame(() => void fitView({ padding: 0.2, maxZoom: 1 }))
    return () => cancelAnimationFrame(frame)
  }, [structure, fitView])

  const containerRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const observer = new ResizeObserver(() => void fitView({ padding: 0.2, maxZoom: 1 }))
    observer.observe(el)
    return () => observer.disconnect()
  }, [fitView])

  return (
    <div
      ref={containerRef}
      className="h-[32rem] min-w-0 overflow-hidden rounded-lg border border-line bg-surface"
      aria-label="Pipeline graph canvas"
    >
      <ReactFlow
        colorMode={theme}
        nodeTypes={nodeTypes}
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onNodeDragStop={onNodeDragStop}
        onNodeClick={(_, node) => {
          const parsed = parseComponentNodeId(node.id)
          if (parsed) onSelect(parsed)
        }}
        onPaneClick={() => onSelect(null)}
        nodesConnectable={false}
        edgesFocusable={false}
        deleteKeyCode={null}
        selectionKeyCode={null}
        multiSelectionKeyCode={null}
        minZoom={0.25}
        maxZoom={1.4}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
        panOnScroll
      >
        <Controls showInteractive={false} position="bottom-right" />
      </ReactFlow>
    </div>
  )
}

/** Tenant signal stream — read-only entry point of the flow. */
function SourceNode({ data }: NodeProps<SourceFlowNode>) {
  const { theme } = useTheme()
  return (
    <div
      className={cn(
        'flex h-11 w-56 items-center gap-2 rounded-full border bg-surface px-3.5 shadow-sm',
        data.invalid ? 'border-danger/60 ring-2 ring-danger/40' : 'border-line',
      )}
    >
      <span
        aria-hidden
        className="size-2 shrink-0 rounded-full"
        style={{ background: SIGNAL_COLOR[data.signal][theme] }}
      />
      <span className="text-[13px] font-medium text-ink">{SIGNAL_LABEL[data.signal]}</span>
      <span className="ml-auto font-mono text-[10px] text-ink-3">tenant stream</span>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  )
}

/** Processor / exporter card: glyph + name, mono instance, config summary. */
function ComponentNode({ data }: NodeProps<ComponentFlowNode>) {
  const Icon = data.section === 'processors' ? SlidersHorizontal : Send
  return (
    <div
      className={cn(
        'flex h-[92px] w-56 cursor-pointer flex-col rounded-lg border bg-surface px-3 py-2 shadow-sm transition-shadow',
        data.invalid
          ? 'border-danger/60 ring-2 ring-danger/50'
          : data.active
            ? 'border-accent/60 ring-2 ring-accent/40'
            : 'border-line',
      )}
    >
      <div className="flex items-center gap-1.5">
        {hasBackendIcon(data.icon) ? (
          <BackendIcon name={data.icon} className="size-3.5 shrink-0 text-ink-3" />
        ) : (
          <Icon aria-hidden className="size-3.5 shrink-0 text-ink-3" />
        )}
        <span className="min-w-0 truncate text-[13px] font-semibold text-ink">{data.label}</span>
        {data.section === 'processors' && (
          <span className="ml-auto font-mono text-[10px] text-ink-3 tabular-nums">
            {data.index + 1}
          </span>
        )}
      </div>
      <div className="truncate font-mono text-[10px] text-ink-3">
        {data.name ?? data.typeId}
      </div>
      <div className="mt-1 flex min-h-0 flex-1 flex-col overflow-hidden">
        {data.summary.length === 0 ? (
          <span className="text-[10px] text-ink-3 italic">default config</span>
        ) : (
          data.summary.map((line) => (
            <span key={line} className="truncate font-mono text-[10px] leading-4 text-ink-2">
              {line}
            </span>
          ))
        )}
      </div>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      {data.section === 'processors' && (
        <Handle type="source" position={Position.Right} isConnectable={false} />
      )}
    </div>
  )
}

/**
 * Right-side editor for the clicked node — the existing schema form over
 * the same draft store slot the form view edits, plus rename/remove.
 */
function NodeEditorPanel({
  selection,
  catalog,
  readOnly,
  onClose,
}: {
  selection: Selection
  catalog: Catalog
  readOnly: boolean
  onClose: () => void
}) {
  const revision = useDraftStore((s) => s.revision)
  const node = useDraftStore((s) => s.graph[selection.section][selection.index])
  const count = useDraftStore((s) => s.graph[selection.section].length)
  const setNodeName = useDraftStore((s) => s.setNodeName)
  const setNodeConfig = useDraftStore((s) => s.setNodeConfig)
  const removeNode = useDraftStore((s) => s.removeNode)

  const components = selection.section === 'processors' ? catalog.processors : catalog.exporters
  const component = node ? components.find((c) => c.type === node.type) : undefined

  if (!node) return null

  const label = component?.displayName ?? node.type
  const removable = !(selection.section === 'exporters' && count <= 1)

  return (
    <aside
      aria-label={`Edit ${label}`}
      className="flex min-w-0 flex-col rounded-lg border border-line bg-surface xl:sticky xl:top-6"
    >
      <header className="flex items-center gap-2 border-b border-line px-4 py-2.5">
        {hasBackendIcon(component?.icon) && (
          <BackendIcon name={component?.icon} className="size-4 shrink-0 text-ink-2" />
        )}
        <div className="min-w-0">
          <h3 className="truncate text-[13px] font-semibold text-ink">{label}</h3>
          <code className="font-mono text-[11px] text-ink-3">{node.type}</code>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="ml-auto h-6.5 w-6.5"
          aria-label="Close node editor"
          onClick={onClose}
        >
          <X />
        </Button>
      </header>
      <div className="flex flex-col gap-4 p-4">
        <div className="flex flex-col gap-1">
          <label
            htmlFor="flow-node-name"
            className="font-mono text-[11px] font-medium text-ink-2 select-none"
          >
            instance name
          </label>
          <Input
            id="flow-node-name"
            placeholder="instance name"
            className="font-mono text-[11px]"
            value={node.name ?? ''}
            disabled={readOnly}
            spellCheck={false}
            onChange={(e) => setNodeName(selection.section, selection.index, e.target.value)}
          />
        </div>

        {component && fieldlessSchema(component.schema as JsonSchema) ? (
          <p className="text-xs text-ink-3">This component has no configuration.</p>
        ) : (
          <SchemaForm
            key={`${revision}-${selection.section}-${selection.index}-${node.type}`}
            schema={(component?.schema ?? {}) as JsonSchema}
            value={node.config}
            onChange={(config) => setNodeConfig(selection.section, selection.index, config)}
            readOnly={readOnly}
            idPrefix={`flow-${selection.section}-${selection.index}`}
          />
        )}

        {!readOnly && (
          <Button
            variant="ghost"
            size="sm"
            className="self-start hover:text-danger"
            disabled={!removable}
            title={removable ? undefined : 'A pipeline needs at least one exporter'}
            onClick={() => {
              removeNode(selection.section, selection.index)
              onClose()
            }}
          >
            <Trash2 aria-hidden />
            Remove {selection.section === 'processors' ? 'processor' : 'exporter'}
          </Button>
        )}
      </div>
    </aside>
  )
}
