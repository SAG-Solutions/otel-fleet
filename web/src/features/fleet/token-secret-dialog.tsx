import { TicketCheck } from 'lucide-react'
import { enrollmentCommand } from '@/features/fleet/enrollment'
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
import type { BootstrapTokenCreated } from '@/api/generated'

/**
 * Show-once bootstrap-token dialog (the API-key SecretDialog pattern) with a
 * ready-to-paste enrollment snippet so the operator never assembles it by hand.
 */
export function TokenSecretDialog({
  token,
  onClose,
}: {
  token: BootstrapTokenCreated | null
  onClose: () => void
}) {
  const command = token ? enrollmentCommand(token.secret) : ''
  return (
    <Dialog
      open={token !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        hideClose
        className="max-w-lg"
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        {token && (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <TicketCheck className="size-4 text-accent" />
                Save your bootstrap token now
              </DialogTitle>
              <DialogDescription>
                This is the only time <span className="font-mono text-ink">{token.name}</span> is
                shown. otel-fleet stores a hash — the token cannot be recovered later.
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-1 rounded-md border border-warn/40 bg-surface-2 p-3">
              <code
                data-testid="bootstrap-token-secret"
                className="min-w-0 flex-1 font-mono text-xs break-all text-ink"
              >
                {token.secret}
              </code>
              <CopyButton value={token.secret} label="Copy bootstrap token" />
            </div>
            <div className="mt-4 flex flex-col gap-1.5">
              <h3 className="text-xs font-semibold text-ink">Enroll an edge agent</h3>
              <p className="text-xs text-ink-3">
                On the customer's host, start the edge agent with the token — it enrolls over OpAMP
                on first connect:
              </p>
              <div className="flex items-start gap-1 rounded-md border border-line bg-surface-2 p-3">
                <code className="min-w-0 flex-1 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-ink-2">
                  <span aria-hidden className="text-accent select-none">
                    ${' '}
                  </span>
                  {command}
                </code>
                <CopyButton value={command} label="Copy enrollment command" />
              </div>
            </div>
            <p className="mt-3 text-xs text-ink-3">
              Token prefix <code className="font-mono">{token.tokenPrefix}</code> identifies it in
              this console.
            </p>
            <DialogFooter>
              <Button variant="primary" onClick={onClose}>
                I saved the token
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
