// Cross-island handoff for seeding the photo grid's search. Islands
// that want the grid to filter by specific tokens write to
// requestedSearchTokens; PhotoGrid watches the ref, copies the value
// into its internal searchTokens state, then clears the handoff so
// the same tokens can be requested again later.
//
// This is intentionally a one-shot signal (not a shared source of
// truth) because PhotoGrid owns the actual search state — it has to,
// since the user can edit the token chips directly after a handoff.

import { ref } from 'vue'

// requestedSearchTokens is null when nothing is pending. Writers set
// it to the token list they want the grid to filter by. PhotoGrid
// resets it to null once consumed.
export const requestedSearchTokens = ref<string[] | null>(null)

export function requestSearchTokens(tokens: string[]): void {
  requestedSearchTokens.value = tokens
}
