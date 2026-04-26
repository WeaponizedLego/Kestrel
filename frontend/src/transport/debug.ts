import { apiGet } from './api'

export interface DebugInfo {
  log_path: string
  log_path_backup: string
  file_size: number
  lines: string[]
  lines_returned: number
  truncated: boolean
}

export function fetchDebugInfo(lines = 200): Promise<DebugInfo> {
  return apiGet<DebugInfo>(`/api/debug?lines=${lines}`)
}
