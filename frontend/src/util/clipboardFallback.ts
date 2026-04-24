// Browser-side clipboard fallback: fetches the image at src, re-encodes
// it through a canvas to PNG, and writes that to the clipboard. Used
// when the backend reports it has no OS clipboard tool available.
// Note: this loses animation for GIF/animated WebP — only the first
// frame survives createImageBitmap + canvas.drawImage.
export async function copyImageViaCanvas(src: string): Promise<void> {
  const res = await fetch(src)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const sourceBlob = await res.blob()
  const bitmap = await createImageBitmap(sourceBlob)
  const canvas = document.createElement('canvas')
  canvas.width = bitmap.width
  canvas.height = bitmap.height
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas 2d context unavailable')
  ctx.drawImage(bitmap, 0, 0)
  bitmap.close?.()
  const pngBlob = await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error('png encode failed'))),
      'image/png',
    )
  })
  await navigator.clipboard.write([new ClipboardItem({ 'image/png': pngBlob })])
}
