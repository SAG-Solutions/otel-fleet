import type { CatalogComponent, CatalogPreset, GraphNode, PipelineGraph, Signal } from '@/api/generated'

/** Sections of the graph that hold ordered component nodes. */
export type NodeSection = 'processors' | 'exporters'

export const EMPTY_GRAPH: PipelineGraph = { signals: [], processors: [], exporters: [] }

function clone<T>(value: T): T {
  return structuredClone(value)
}

/** Instantiate a node from a catalog entry, seeded with its defaults. */
export function nodeFromCatalog(component: CatalogComponent): GraphNode {
  return { type: component.type, config: clone(component.defaults ?? {}) }
}

/**
 * Instantiate an exporter node from a backend preset: the underlying
 * exporter type with the preset's pre-filled config. Mirrors
 * {@link nodeFromCatalog}'s defaults-cloning so preset and raw-catalog nodes
 * are seeded identically.
 */
export function nodeFromPreset(preset: CatalogPreset): GraphNode {
  return { type: preset.exporterType, config: clone(preset.defaults ?? {}) }
}

/**
 * The graph a brand-new pipeline starts with: logs through a batch processor
 * into the debug exporter. Catalog defaults are used when available.
 */
export function defaultGraph(catalog?: {
  processors: CatalogComponent[]
  exporters: CatalogComponent[]
}): PipelineGraph {
  const batch = catalog?.processors.find((c) => c.type === 'batch')
  const debug = catalog?.exporters.find((c) => c.type === 'debug')
  return {
    signals: ['logs'],
    processors: [batch ? nodeFromCatalog(batch) : { type: 'batch', config: {} }],
    exporters: [debug ? nodeFromCatalog(debug) : { type: 'debug', config: {} }],
  }
}

/* Pure graph transforms — the zustand store delegates to these. */

export function toggleSignal(graph: PipelineGraph, signal: Signal): PipelineGraph {
  const has = graph.signals.includes(signal)
  return {
    ...graph,
    signals: has ? graph.signals.filter((s) => s !== signal) : [...graph.signals, signal],
  }
}

export function addNode(graph: PipelineGraph, section: NodeSection, node: GraphNode): PipelineGraph {
  return { ...graph, [section]: [...graph[section], node] }
}

export function removeNode(graph: PipelineGraph, section: NodeSection, index: number): PipelineGraph {
  return { ...graph, [section]: graph[section].filter((_, i) => i !== index) }
}

/** Move a node up (-1) or down (+1); out-of-range moves are no-ops. */
export function moveNode(
  graph: PipelineGraph,
  section: NodeSection,
  index: number,
  delta: -1 | 1,
): PipelineGraph {
  const target = index + delta
  if (index < 0 || index >= graph[section].length || target < 0 || target >= graph[section].length) {
    return graph
  }
  const nodes = [...graph[section]]
  const [node] = nodes.splice(index, 1)
  if (!node) {
    return graph
  }
  nodes.splice(target, 0, node)
  return { ...graph, [section]: nodes }
}

/**
 * Reorder a section by an explicit permutation of current indices
 * (e.g. [2, 0, 1] puts the third node first). Non-permutations are no-ops
 * so a bad drag result can never lose nodes.
 */
export function reorderNodes(
  graph: PipelineGraph,
  section: NodeSection,
  order: number[],
): PipelineGraph {
  const nodes = graph[section]
  if (order.length !== nodes.length) return graph
  if ([...order].sort((a, b) => a - b).some((value, i) => value !== i)) return graph
  const reordered = order.map((i) => nodes[i]!)
  return { ...graph, [section]: reordered }
}

export function updateNodeConfig(
  graph: PipelineGraph,
  section: NodeSection,
  index: number,
  config: Record<string, unknown>,
): PipelineGraph {
  return {
    ...graph,
    [section]: graph[section].map((node, i) => (i === index ? { ...node, config } : node)),
  }
}

export function renameNode(
  graph: PipelineGraph,
  section: NodeSection,
  index: number,
  name: string,
): PipelineGraph {
  const trimmed = name.trim()
  return {
    ...graph,
    [section]: graph[section].map((node, i) =>
      i === index ? { ...node, name: trimmed === '' ? null : trimmed } : node,
    ),
  }
}
