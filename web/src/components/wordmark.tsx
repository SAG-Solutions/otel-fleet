import { cn } from '@/lib/utils'

/**
 * The otel-fleet wordmark: three ascending signal bars (logs/traces/metrics
 * pulse) plus the lowercase mono name with the fleet half in signal-amber.
 */
export function Wordmark({ className, large = false }: { className?: string; large?: boolean }) {
  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <span aria-hidden className={cn('flex items-end', large ? 'gap-[3px]' : 'gap-0.5')}>
        <span className={cn('w-1 rounded-[1px] bg-accent/50', large ? 'h-2.5' : 'h-1.5')} />
        <span className={cn('w-1 rounded-[1px] bg-accent/75', large ? 'h-4' : 'h-2.5')} />
        <span className={cn('w-1 rounded-[1px] bg-accent', large ? 'h-5.5' : 'h-3.5')} />
      </span>
      <span
        className={cn(
          'font-mono font-semibold tracking-tight text-ink lowercase',
          large ? 'text-2xl' : 'text-[15px]',
        )}
      >
        otel<span className="text-accent">fleet</span>
      </span>
    </span>
  )
}
