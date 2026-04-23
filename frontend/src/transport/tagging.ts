// Cross-island event helpers for the tagging + similarity-review
// surfaces. The Toolbar island fires the open events; the
// TaggingQueue and SimilarityReview islands listen for them. Using
// window CustomEvents instead of a shared store keeps islands
// independent (each hydrates in isolation) and matches the pattern
// Kestrel already uses for transport events.

const OPEN_TAGGING = 'kestrel:open-tagging-queue'
const OPEN_SIMILARITY = 'kestrel:open-similarity-review'
const OPEN_TAG_MANAGER = 'kestrel:open-tag-manager'

export type SimilarityTab = 'duplicate' | 'similar'

export function openTaggingQueue(): void {
  window.dispatchEvent(new CustomEvent(OPEN_TAGGING))
}

export function onOpenTaggingQueue(fn: () => void): () => void {
  const handler = () => fn()
  window.addEventListener(OPEN_TAGGING, handler)
  return () => window.removeEventListener(OPEN_TAGGING, handler)
}

// openSimilarityReview opens the merged duplicates / similar window.
// Pass 'duplicate' or 'similar' to preselect a tab; omit to keep the
// currently active tab.
export function openSimilarityReview(tab?: SimilarityTab): void {
  window.dispatchEvent(new CustomEvent(OPEN_SIMILARITY, { detail: tab }))
}

export function onOpenSimilarityReview(
  fn: (tab?: SimilarityTab) => void,
): () => void {
  const handler = (e: Event) => {
    const tab = (e as CustomEvent<SimilarityTab | undefined>).detail
    fn(tab)
  }
  window.addEventListener(OPEN_SIMILARITY, handler)
  return () => window.removeEventListener(OPEN_SIMILARITY, handler)
}

export function openTagManager(): void {
  window.dispatchEvent(new CustomEvent(OPEN_TAG_MANAGER))
}

export function onOpenTagManager(fn: () => void): () => void {
  const handler = () => fn()
  window.addEventListener(OPEN_TAG_MANAGER, handler)
  return () => window.removeEventListener(OPEN_TAG_MANAGER, handler)
}
