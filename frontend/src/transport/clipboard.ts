// copyImageToClipboard puts the image at the given path on the OS
// clipboard. It first asks the backend, which can use a native tool
// (xclip, wl-copy, pbcopy, PowerShell), and falls back to a
// browser-side canvas re-encode if the backend reports it has no
// clipboard tool. Both islands that copy images call this — the
// PhotoViewer detail panel and the PhotoGrid right-click menu.

import { apiPost, photoSrc } from './api'
import { copyImageViaCanvas } from '../util/clipboardFallback'

export async function copyImageToClipboard(path: string): Promise<void> {
  try {
    await apiPost<{ copied: boolean }>('/api/clipboard/copy', { path })
    return
  } catch (backendErr) {
    try {
      await copyImageViaCanvas(photoSrc(path))
      return
    } catch {
      throw backendErr
    }
  }
}
