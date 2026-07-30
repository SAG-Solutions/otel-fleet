import { Share2 } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * Bundled inline-SVG brand marks for the backend catalog. The app runs under a
 * strict CSP that forbids external/CDN images, so every glyph ships as a React
 * component. These are simplified, tasteful marks — recognizable per backend,
 * not pixel-exact trademarked logos — drawn on a 24×24 canvas and inheriting
 * `currentColor` so they tint with the surrounding text.
 *
 * `<BackendIcon name>` maps an `icon` key from the component catalog
 * (`loki`, `tempo`, …) to its glyph; an unknown or empty key falls back to a
 * generic "export" mark.
 */

type IconProps = { className?: string }

const SVG_BASE = 'shrink-0'

function Svg({
  className,
  strokeWidth = 1.75,
  children,
}: IconProps & { strokeWidth?: number; children: React.ReactNode }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      className={cn(SVG_BASE, className)}
    >
      {children}
    </svg>
  )
}

/** Loki — stacked log lines. */
function LokiIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <line x1="4" y1="6" x2="20" y2="6" />
      <line x1="4" y1="10" x2="16" y2="10" />
      <line x1="4" y1="14" x2="19" y2="14" />
      <line x1="4" y1="18" x2="13" y2="18" />
    </Svg>
  )
}

/** Tempo — a trace/span waterfall. */
function TempoIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <rect x="4" y="4.5" width="15" height="3" rx="1.5" />
      <rect x="7" y="10.5" width="12" height="3" rx="1.5" />
      <rect x="10" y="16.5" width="9" height="3" rx="1.5" />
    </Svg>
  )
}

/** Prometheus — torch flame over a rack base. */
function PrometheusIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M12 3c1.6 2.4.4 4-.6 5.4-1 1.4-1.3 2.9.6 4.6.4-1 1-1.7 1.6-2.1.7 2.1.3 3.6-.4 4.7 2.2-1 3.4-3 3.4-5.4C18.2 7.3 15 5.4 12 3Z" />
      <path d="M6.5 15.5h11" />
      <rect x="7" y="17.5" width="10" height="3.5" rx="1" />
    </Svg>
  )
}

/** Grafana — orb on a base (the "ball" mark). */
function GrafanaIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx="12" cy="8" r="4" />
      <path d="M12 12v4" />
      <path d="M6 20c0-3 2.7-4.5 6-4.5s6 1.5 6 4.5" />
    </Svg>
  )
}

/** Jaeger — a compass (tracing/pathfinding). */
function JaegerIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M15.5 8.5 13 13l-4.5 2.5L11 11l4.5-2.5Z" />
    </Svg>
  )
}

/** OTLP / OpenTelemetry — hexagon with a center node. */
function OtlpIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M12 3l7.5 4.3v9.4L12 21l-7.5-4.3V7.3L12 3Z" />
      <circle cx="12" cy="12" r="2.5" fill="currentColor" stroke="none" />
    </Svg>
  )
}

/** Elasticsearch — segmented ring. */
function ElasticsearchIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M12 3.5a8.5 8.5 0 0 1 7 4H8" />
      <path d="M4 10.5h16" />
      <path d="M4.5 14.5h15" />
      <path d="M5 18a8.5 8.5 0 0 0 14-2H10" />
    </Svg>
  )
}

/** Datadog — a paw print. */
function DatadogIcon(props: IconProps) {
  return (
    <Svg {...props} strokeWidth={1.25}>
      <ellipse cx="7" cy="8" rx="1.6" ry="2.2" fill="currentColor" stroke="none" />
      <ellipse cx="12" cy="6.5" rx="1.6" ry="2.2" fill="currentColor" stroke="none" />
      <ellipse cx="17" cy="8" rx="1.6" ry="2.2" fill="currentColor" stroke="none" />
      <path
        d="M12 11c2.4 0 4.5 1.7 4.5 3.8 0 1.7-1.6 2.7-4.5 2.7s-4.5-1-4.5-2.7C7.5 12.7 9.6 11 12 11Z"
        fill="currentColor"
        stroke="none"
      />
    </Svg>
  )
}

/** Kafka — connected nodes (a broker cluster). */
function KafkaIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <circle cx="6" cy="12" r="2.5" />
      <circle cx="17" cy="6.5" r="2.5" />
      <circle cx="17" cy="17.5" r="2.5" />
      <path d="M8.2 10.8 14.8 7.7" />
      <path d="M8.2 13.2 14.8 16.3" />
    </Svg>
  )
}

/** AWS S3 — a storage bucket. */
function AwsS3Icon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M4.5 5.5h15l-1.5 13.5a1 1 0 0 1-1 .9H7a1 1 0 0 1-1-.9L4.5 5.5Z" />
      <path d="M4.5 5.5c0 1.1 3.4 2 7.5 2s7.5-.9 7.5-2S16.1 3.5 12 3.5 4.5 4.4 4.5 5.5Z" />
    </Svg>
  )
}

/** Splunk — the chevron mark. */
function SplunkIcon(props: IconProps) {
  return (
    <Svg {...props} strokeWidth={2.25}>
      <path d="M6 5l9 7-9 7" />
    </Svg>
  )
}

const ICONS: Record<string, (props: IconProps) => React.ReactElement> = {
  loki: LokiIcon,
  tempo: TempoIcon,
  prometheus: PrometheusIcon,
  grafana: GrafanaIcon,
  jaeger: JaegerIcon,
  otlp: OtlpIcon,
  elasticsearch: ElasticsearchIcon,
  datadog: DatadogIcon,
  kafka: KafkaIcon,
  awss3: AwsS3Icon,
  splunk: SplunkIcon,
}

/** The icon keys with a dedicated brand mark (everything else falls back). */
export const BACKEND_ICON_KEYS = Object.keys(ICONS)

/** True when `name` has a dedicated brand mark (i.e. not the fallback). */
export function hasBackendIcon(name?: string | null): boolean {
  return name != null && name in ICONS
}

/**
 * Brand mark for a backend catalog `icon` key. Unknown / empty keys render a
 * generic "export" glyph so a card always has something to show.
 */
export function BackendIcon({ name, className }: { name?: string | null; className?: string }) {
  const Glyph = name ? ICONS[name] : undefined
  if (!Glyph) return <Share2 aria-hidden className={cn(SVG_BASE, className)} />
  return <Glyph className={className} />
}
