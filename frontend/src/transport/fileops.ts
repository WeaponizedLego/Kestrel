// Cross-island helpers for destructive file operations. The Toolbar
// fires CustomEvents; the FileOps island listens, runs the actual
// workflow (folder picker, confirmation modal, undo toast), and
// talks to the backend.
//
// Keeping the API client alongside the CustomEvent helpers mirrors
// transport/tagging.ts — islands import only what they need.

import { apiGet, apiPost } from './api'

export interface FileOpResult {
  path: string
  success: boolean
  error?: string
  new_path?: string
  trash_id?: string
}

export interface MoveResponse {
  moved: number
  failed: number
  results: FileOpResult[]
}

export interface DeleteResponse {
  deleted: number
  failed: number
  results: FileOpResult[]
}

export interface UndoResponse {
  undone: { id: string; kind: string; items: unknown[] }
  results: FileOpResult[]
  remaining: number
}

export function apiMove(paths: string[], dest: string, verify: boolean): Promise<MoveResponse> {
  return apiPost<MoveResponse>('/api/files/move', { paths, dest, verify })
}

export function apiDelete(paths: string[], permanent: boolean): Promise<DeleteResponse> {
  return apiPost<DeleteResponse>('/api/files/delete', { paths, permanent })
}

export function apiUndo(): Promise<UndoResponse> {
  return apiPost<UndoResponse>('/api/files/undo', {})
}

export function apiUndoDepth(): Promise<{ depth: number }> {
  return apiGet<{ depth: number }>('/api/files/undo/depth')
}

// ---- CustomEvent bridge (Toolbar → FileOps island) ----

const REQUEST_MOVE = 'kestrel:request-move'
const REQUEST_DELETE = 'kestrel:request-delete'
const REQUEST_UNDO = 'kestrel:request-undo'

export interface MoveRequestDetail {
  paths: string[]
}
export interface DeleteRequestDetail {
  paths: string[]
}

export function requestMove(paths: string[]): void {
  window.dispatchEvent(new CustomEvent<MoveRequestDetail>(REQUEST_MOVE, { detail: { paths } }))
}

export function requestDelete(paths: string[]): void {
  window.dispatchEvent(new CustomEvent<DeleteRequestDetail>(REQUEST_DELETE, { detail: { paths } }))
}

export function requestUndo(): void {
  window.dispatchEvent(new CustomEvent(REQUEST_UNDO))
}

export function onRequestMove(fn: (detail: MoveRequestDetail) => void): () => void {
  const handler = (e: Event) => fn((e as CustomEvent<MoveRequestDetail>).detail)
  window.addEventListener(REQUEST_MOVE, handler)
  return () => window.removeEventListener(REQUEST_MOVE, handler)
}

export function onRequestDelete(fn: (detail: DeleteRequestDetail) => void): () => void {
  const handler = (e: Event) => fn((e as CustomEvent<DeleteRequestDetail>).detail)
  window.addEventListener(REQUEST_DELETE, handler)
  return () => window.removeEventListener(REQUEST_DELETE, handler)
}

export function onRequestUndo(fn: () => void): () => void {
  const handler = () => fn()
  window.addEventListener(REQUEST_UNDO, handler)
  return () => window.removeEventListener(REQUEST_UNDO, handler)
}
