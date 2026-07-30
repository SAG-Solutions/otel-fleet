import { useState } from 'react'
import { ExternalLink, Plus } from 'lucide-react'
import { useDraftStore } from '@/features/pipelines/draft-store'
import { nodeFromCatalog, nodeFromPreset } from '@/features/pipelines/graph'
import { BackendIcon } from '@/features/pipelines/backend-icons'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { CatalogComponent, CatalogPreset } from '@/api/generated'

const TABS = ['presets', 'all'] as const
type GalleryTab = (typeof TABS)[number]

/**
 * "Add exporter" affordance: opens a gallery dialog that offers a one-click
 * grid of backend presets (Loki, Tempo, Datadog, …) alongside the full list
 * of raw exporters. Both paths add an exporter node to the draft graph and
 * seed its config exactly like the rest of the builder — presets via
 * {@link nodeFromPreset}, raw exporters via {@link nodeFromCatalog} — so
 * validation, YAML preview and signal wiring behave identically.
 */
export function ExporterGallery({
  presets,
  exporters,
  onAdded,
}: {
  presets: CatalogPreset[]
  exporters: CatalogComponent[]
  /** Called with the index of the freshly added exporter node. */
  onAdded?: (index: number) => void
}) {
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<GalleryTab>(presets.length > 0 ? 'presets' : 'all')
  const addNode = useDraftStore((s) => s.addNode)

  const add = (node: ReturnType<typeof nodeFromPreset>) => {
    const index = useDraftStore.getState().graph.exporters.length
    addNode('exporters', node)
    setOpen(false)
    onAdded?.(index)
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <Plus aria-hidden />
        Add exporter
      </Button>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add an exporter</DialogTitle>
          <DialogDescription>
            Send this pipeline to a backend. Pick a ready-made preset or configure any exporter
            from scratch.
          </DialogDescription>
        </DialogHeader>

        {presets.length > 0 && (
          <div
            role="tablist"
            aria-label="Exporter source"
            className="mb-1 inline-flex h-8 items-center gap-0.5 self-start rounded-md border border-line bg-surface p-0.5"
          >
            {TABS.map((value) => (
              <button
                key={value}
                type="button"
                role="tab"
                aria-selected={tab === value}
                onClick={() => setTab(value)}
                className={cn(
                  'inline-flex h-6.5 cursor-pointer items-center rounded px-2.5 text-xs transition-colors outline-none focus-visible:ring-2 focus-visible:ring-accent/70',
                  tab === value
                    ? 'bg-surface-2 font-semibold text-ink'
                    : 'text-ink-3 hover:text-ink-2',
                )}
              >
                {value === 'presets' ? 'Popular backends' : 'All exporters'}
              </button>
            ))}
          </div>
        )}

        <div className="max-h-[60vh] overflow-y-auto pr-1">
          {tab === 'presets' && presets.length > 0 ? (
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              {presets.map((preset) => (
                <PresetCard key={preset.id} preset={preset} onSelect={() => add(nodeFromPreset(preset))} />
              ))}
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              {exporters.length === 0 && (
                <p className="px-1 py-2 text-xs text-ink-3">Catalog unavailable.</p>
              )}
              {exporters.map((component) => (
                <ExporterRow
                  key={component.type}
                  component={component}
                  onSelect={() => add(nodeFromCatalog(component))}
                />
              ))}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

function PresetCard({ preset, onSelect }: { preset: CatalogPreset; onSelect: () => void }) {
  return (
    <div className="group relative flex flex-col rounded-lg border border-line bg-surface p-3 transition-colors hover:border-accent/60 hover:bg-surface-2">
      <button
        type="button"
        onClick={onSelect}
        className="flex items-start gap-2.5 text-left outline-none after:absolute after:inset-0 after:rounded-lg focus-visible:after:ring-2 focus-visible:after:ring-accent/70"
      >
        <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-line bg-surface text-ink-2">
          <BackendIcon name={preset.icon} className="size-5" />
        </span>
        <span className="min-w-0">
          <span className="block text-[13px] font-semibold text-ink">{preset.displayName}</span>
          <span className="mt-0.5 line-clamp-2 block text-xs text-ink-2">{preset.description}</span>
        </span>
      </button>
      {preset.docsUrl && (
        <a
          href={preset.docsUrl}
          target="_blank"
          rel="noreferrer"
          onClick={(e) => e.stopPropagation()}
          className="relative z-10 mt-2 inline-flex w-fit items-center gap-1 rounded text-[11px] text-ink-3 outline-none hover:text-accent focus-visible:ring-2 focus-visible:ring-accent/70"
        >
          <ExternalLink className="size-3" aria-hidden />
          Docs
        </a>
      )}
    </div>
  )
}

function ExporterRow({
  component,
  onSelect,
}: {
  component: CatalogComponent
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className="flex items-start gap-2.5 rounded-md border border-transparent px-2.5 py-2 text-left outline-none transition-colors hover:border-line hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-accent/70"
    >
      <span className="flex size-7 shrink-0 items-center justify-center rounded-md border border-line bg-surface text-ink-2">
        <BackendIcon name={component.icon} className="size-4" />
      </span>
      <span className="min-w-0">
        <span className="flex items-baseline gap-2">
          <span className="text-[13px] font-medium text-ink">{component.displayName}</span>
          <span className="font-mono text-[11px] text-ink-3">{component.type}</span>
        </span>
        <span className="mt-0.5 line-clamp-2 block text-xs text-ink-2">{component.description}</span>
      </span>
    </button>
  )
}
