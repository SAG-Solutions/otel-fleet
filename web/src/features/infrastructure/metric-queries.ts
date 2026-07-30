import { apiErrorMessage } from '@/lib/api-error'
import { formatCompact } from '@/lib/format'

/** A curated Infrastructure panel: a title plus the PromQL it charts. */
export interface MetricPanelDef {
  id: string
  title: string
  query: string
}

/**
 * Default cluster-monitoring panels. Metric names follow OTel semconv
 * (underscored after remote-write) and MAY vary by collector version — each
 * panel shows a graceful empty state when the series come back empty.
 */
export const INFRA_PANELS: readonly MetricPanelDef[] = [
  {
    id: 'node-cpu',
    title: 'Node CPU utilization',
    query: 'avg by (k8s_node_name) (k8s_node_cpu_utilization)',
  },
  {
    id: 'node-memory',
    title: 'Node memory usage',
    query: 'sum by (k8s_node_name) (k8s_node_memory_usage)',
  },
  {
    id: 'pods-by-namespace',
    title: 'Pods by namespace',
    query: 'count by (k8s_namespace_name) (k8s_pod_phase)',
  },
  {
    id: 'container-restarts',
    title: 'Container restarts (top 10)',
    query: 'topk(10, k8s_container_restarts)',
  },
  {
    id: 'top-pods-cpu',
    title: 'Top pods by CPU',
    query: 'topk(10, k8s_pod_cpu_utilization)',
  },
] as const

/** Most-specific-first label keys used to name a series in the legend. */
const PREFERRED_LABELS = [
  'k8s_pod_name',
  'k8s_container_name',
  'k8s_node_name',
  'k8s_namespace_name',
] as const

/**
 * Legend name for a PromQL result series: the most specific well-known label
 * (pod → container → node → namespace), falling back to the full label set,
 * then to a generic "value" for a scalar/aggregate with no labels.
 */
export function seriesLabel(labels: Record<string, string>): string {
  for (const key of PREFERRED_LABELS) {
    if (labels[key]) return labels[key]
  }
  const entries = Object.entries(labels)
  if (entries.length === 0) return 'value'
  return entries.map(([k, v]) => `${k}="${v}"`).join(', ')
}

/** Compact axis/tooltip value: fractions keep 3 dp, integers stay plain. */
export function formatMetricValue(value: number): string {
  if (!Number.isFinite(value)) return '—'
  const abs = Math.abs(value)
  if (abs !== 0 && abs < 1) return value.toFixed(3)
  if (abs < 1000) return Number.isInteger(value) ? value.toString() : value.toFixed(1)
  return formatCompact(value)
}

/**
 * Human detail for a failed query_range call. The store-unavailable (503)
 * error carries code `upstream_unavailable`; everything else (a 400 with a
 * PromQL parse error, say) surfaces its own message.
 */
export function metricErrorDetail(error: unknown): string {
  if (
    error !== null &&
    typeof error === 'object' &&
    'code' in error &&
    (error as { code: unknown }).code === 'upstream_unavailable'
  ) {
    return 'Metrics store unavailable — VictoriaMetrics is not reachable.'
  }
  return apiErrorMessage(error, 'The query could not be run.')
}
