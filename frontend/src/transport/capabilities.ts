import { ref } from 'vue'
import { apiGet } from './api'

// Capabilities mirrors api.capabilitiesResponse. False is the safe
// default — every consumer treats a missing tool as "feature not
// available" and degrades gracefully (placeholder thumbnails, no
// playback, etc.).
export interface Capabilities {
  ffmpeg: boolean
  ffprobe: boolean
}

const state = ref<Capabilities>({ ffmpeg: false, ffprobe: false })
let loaded = false
let inflight: Promise<void> | null = null

// useCapabilities returns the shared reactive Capabilities ref. The
// first caller triggers a single GET /api/capabilities; subsequent
// callers reuse the same fetch. The endpoint is a couple of
// exec.LookPath calls server-side, so we don't aggressively cache —
// the frontend asks once per page load and re-asks if the user
// reloads.
export function useCapabilities() {
  if (!loaded && !inflight) {
    inflight = apiGet<Capabilities>('/api/capabilities')
      .then((c) => {
        state.value = c
        loaded = true
      })
      .catch(() => {
        // Endpoint unreachable — keep defaults (all false). The
        // banner will show; the worst case is the user is told to
        // install ffmpeg they already have.
      })
      .finally(() => {
        inflight = null
      })
  }
  return state
}
