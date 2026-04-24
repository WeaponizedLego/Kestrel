// Transport helpers for the kestrel-vision sidecar status endpoint.
// Kept as a tiny module so the StatusBar badge and TaggingQueue faces
// tab share one fetch path and one shape.

import { apiGet } from './api'

export type VisionState = 'unknown' | 'off' | 'error' | 'on'

export interface VisionStatus {
  state: VisionState
  version?: string
  models?: string[]
  lastError?: string
  checkedAt: number
  // inFlight counts /detect calls currently running on the sidecar.
  // > 0 means the sidecar is actively processing images (typically
  // during a scan) — used by the StatusBar badge to distinguish
  // "on and busy" from "on and idle".
  inFlight: number
  detectCount: number
  lastDetect?: number
}

// fetchVisionStatus pulls the latest cached status. Pass refresh=true
// when the user clicks the badge to force a fresh probe on the
// backend before the next background tick.
export async function fetchVisionStatus(refresh = false): Promise<VisionStatus> {
  const path = refresh ? '/api/vision/status?refresh=1' : '/api/vision/status'
  return apiGet<VisionStatus>(path)
}
