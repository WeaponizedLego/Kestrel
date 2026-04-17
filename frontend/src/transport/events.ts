// Singleton WebSocket client for server-pushed events. Islands call
// onEvent(kind, fn) at mount time and get an unsubscribe function
// back; the transport handles connection, reconnection, and fan-out.
//
// The protocol is one-way: the server sends {kind, payload} JSON
// frames, and we never write back. Reconnection uses exponential
// backoff capped at reconnectMaxMs so a dead backend doesn't spin.

type Listener = (payload: unknown) => void

interface ServerEvent {
  kind: string
  payload: unknown
}

const reconnectInitialMs = 500
const reconnectMaxMs = 10_000

const listeners = new Map<string, Set<Listener>>()
let socket: WebSocket | null = null
let reconnectDelay = reconnectInitialMs
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let started = false

function readToken(): string {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="kestrel-token"]')
  if (!meta) {
    throw new Error('kestrel-token meta tag missing — is the page served by the Go binary?')
  }
  return meta.content
}

function wsUrl(): string {
  const token = encodeURIComponent(readToken())
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws?token=${token}`
}

function dispatch(e: ServerEvent): void {
  const bucket = listeners.get(e.kind)
  if (!bucket) return
  for (const fn of bucket) {
    try {
      fn(e.payload)
    } catch (err) {
      console.error(`events: listener for ${e.kind} threw`, err)
    }
  }
}

function scheduleReconnect(): void {
  if (reconnectTimer !== null) return
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    connect()
  }, reconnectDelay)
  reconnectDelay = Math.min(reconnectDelay * 2, reconnectMaxMs)
}

function connect(): void {
  try {
    socket = new WebSocket(wsUrl())
  } catch (err) {
    console.error('events: failed to open socket', err)
    scheduleReconnect()
    return
  }

  socket.addEventListener('open', () => {
    reconnectDelay = reconnectInitialMs
  })

  socket.addEventListener('message', (ev) => {
    try {
      const parsed = JSON.parse(String(ev.data)) as ServerEvent
      if (typeof parsed?.kind === 'string') dispatch(parsed)
    } catch (err) {
      console.error('events: bad frame', err)
    }
  })

  socket.addEventListener('close', () => {
    socket = null
    scheduleReconnect()
  })

  socket.addEventListener('error', () => {
    // onclose will follow and schedule the retry; nothing extra to do.
  })
}

function ensureConnected(): void {
  if (started) return
  started = true
  connect()
}

export function onEvent(kind: string, fn: Listener): () => void {
  ensureConnected()
  let bucket = listeners.get(kind)
  if (!bucket) {
    bucket = new Set()
    listeners.set(kind, bucket)
  }
  bucket.add(fn)
  return () => {
    const b = listeners.get(kind)
    if (!b) return
    b.delete(fn)
    if (b.size === 0) listeners.delete(kind)
  }
}
