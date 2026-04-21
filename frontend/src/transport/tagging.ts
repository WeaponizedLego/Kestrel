// Cross-island event helpers for the assisted-tagging + duplicate
// surfaces. The Toolbar island fires the open events; the
// TaggingQueue and Duplicates islands listen for them. Using window
// CustomEvents instead of a shared store keeps islands independent
// (each hydrates in isolation) and matches the pattern Kestrel
// already uses for transport events.

const OPEN_TAGGING = 'kestrel:open-tagging-queue'
const OPEN_DUPLICATES = 'kestrel:open-duplicates'
const OPEN_TAG_MANAGER = 'kestrel:open-tag-manager'

export function openTaggingQueue(): void {
  window.dispatchEvent(new CustomEvent(OPEN_TAGGING))
}

export function onOpenTaggingQueue(fn: () => void): () => void {
  const handler = () => fn()
  window.addEventListener(OPEN_TAGGING, handler)
  return () => window.removeEventListener(OPEN_TAGGING, handler)
}

export function openDuplicates(): void {
  window.dispatchEvent(new CustomEvent(OPEN_DUPLICATES))
}

export function onOpenDuplicates(fn: () => void): () => void {
  const handler = () => fn()
  window.addEventListener(OPEN_DUPLICATES, handler)
  return () => window.removeEventListener(OPEN_DUPLICATES, handler)
}

export function openTagManager(): void {
  window.dispatchEvent(new CustomEvent(OPEN_TAG_MANAGER))
}

export function onOpenTagManager(fn: () => void): () => void {
  const handler = () => fn()
  window.addEventListener(OPEN_TAG_MANAGER, handler)
  return () => window.removeEventListener(OPEN_TAG_MANAGER, handler)
}
