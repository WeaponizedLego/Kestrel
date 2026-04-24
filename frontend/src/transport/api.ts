// Shared REST client for talking to the Go backend. Every request
// carries the per-run session token the Go server injected into
// index.html as a <meta> tag.

function readToken(): string {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="kestrel-token"]')
  if (!meta || !meta.content) {
    // Logging instead of throwing keeps the rest of the island
    // mountable: the user sees the toolbar + an error state from the
    // first API call rather than a blank screen with a console
    // exception.
    console.warn('kestrel-token meta tag missing — API calls will fail')
    return ''
  }
  return meta.content
}

export const token = readToken()

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { 'X-Kestrel-Token': token } })
  if (!res.ok) throw await apiError(path, res)
  return res.json() as Promise<T>
}

export async function apiPost<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: {
      'X-Kestrel-Token': token,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw await apiError(path, res)
  return res.json() as Promise<T>
}

export async function apiPut<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'PUT',
    headers: {
      'X-Kestrel-Token': token,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw await apiError(path, res)
  return res.json() as Promise<T>
}

// apiError builds a single Error whose message is the server's JSON
// {"error": "..."} payload when present, falling back to the status
// line. Callers surface .message directly to users, so keep it short
// and punctuation-free.
async function apiError(path: string, res: Response): Promise<Error> {
  try {
    const body = (await res.json()) as { error?: string }
    if (body?.error) return new Error(body.error)
  } catch {
    // response wasn't JSON — fall through.
  }
  return new Error(`${path} failed (${res.status} ${res.statusText})`)
}

// friendlyError turns a thrown value into a user-facing string. Used
// by islands to render toasts / inline messages without a "Error: "
// prefix or a stack trace leaking through.
export function friendlyError(err: unknown): string {
  if (err instanceof TypeError) return 'Unable to reach the server.'
  if (err instanceof Error) return err.message
  return 'Something went wrong.'
}

// photoSrc returns the full-res /api/photo URL. The token rides as a
// query param so <img src> can point at it directly; on loopback
// that's acceptable and simpler than a fetch+blob round-trip.
export function photoSrc(path: string): string {
  const params = new URLSearchParams({ path, token })
  return `/api/photo?${params.toString()}`
}
