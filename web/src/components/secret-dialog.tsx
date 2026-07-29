import { KeyRound } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/copy-button'
import type { ApiKeyCreated } from '@/api/generated'

/**
 * Show-once API key dialog. The secret is never retrievable again, so the
 * dialog closes only through the explicit acknowledgement button.
 */
export function SecretDialog({
  apiKey,
  onClose,
}: {
  apiKey: ApiKeyCreated | null
  onClose: () => void
}) {
  return (
    <Dialog
      open={apiKey !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        hideClose
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        {apiKey && (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <KeyRound className="size-4 text-accent" />
                Save your API key now
              </DialogTitle>
              <DialogDescription>
                This is the only time <span className="font-mono text-ink">{apiKey.name}</span> is
                shown. otel-fleet stores a hash — the key cannot be recovered later.
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-1 rounded-md border border-warn/40 bg-surface-2 p-3">
              <code
                data-testid="api-key-secret"
                className="min-w-0 flex-1 font-mono text-xs break-all text-ink"
              >
                {apiKey.secret}
              </code>
              <CopyButton value={apiKey.secret} label="Copy API key" />
            </div>
            <p className="mt-3 text-xs text-ink-3">
              Use it as{' '}
              <code className="font-mono">authorization=&quot;Bearer &lt;key&gt;&quot;</code> on
              OTLP exports. Key prefix <code className="font-mono">{apiKey.keyPrefix}</code>{' '}
              identifies it in this console.
            </p>
            <DialogFooter>
              <Button variant="primary" onClick={onClose}>
                I saved the key
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
