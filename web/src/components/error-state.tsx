import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'

/** Inline error panel for a failed query: what went wrong plus a retry. */
export function ErrorState({
  title = 'Could not load data',
  detail,
  onRetry,
}: {
  title?: string
  detail?: string
  onRetry?: () => void
}) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center gap-3 rounded-lg border border-danger/30 bg-danger/5 px-6 py-8 text-center"
    >
      <AlertTriangle className="size-5 text-danger" />
      <div>
        <div className="text-sm font-semibold text-ink">{title}</div>
        <div className="mt-1 text-[13px] text-ink-2">
          {detail ?? 'The request failed. Check that the otel-fleet API is reachable, then retry.'}
        </div>
      </div>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          Retry
        </Button>
      )}
    </div>
  )
}
