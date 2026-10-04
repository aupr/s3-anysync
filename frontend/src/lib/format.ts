import type { ActionState, Direction, SyncResult } from './types'

export const isZeroTime = (s?: string) => !s || s.startsWith('0001-01-01')

export function formatTime(s?: string): string {
  if (isZeroTime(s)) return '—'
  const d = new Date(s!)
  return Number.isNaN(d.getTime()) ? s! : d.toLocaleString()
}

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

export const stateLabels: Record<ActionState, string> = {
  idle: 'Idle',
  running: 'Running',
  'paused-conflict': 'Conflicts',
  'paused-deletes': 'Deletions to review',
  paused: 'Paused',
  error: 'Error',
}

export type Tone = 'neutral' | 'ok' | 'warn' | 'error'

export function stateTone(s: ActionState): Tone {
  if (s === 'error') return 'error'
  if (s === 'paused-conflict' || s === 'paused-deletes') return 'warn'
  if (s === 'idle') return 'ok'
  return 'neutral'
}

export const directionLabels: Record<Direction, string> = {
  'two-way': 'Two-way',
  'local-to-cloud': 'This machine → cloud',
  'cloud-to-local': 'Cloud → this machine',
}

// summarizeResult mirrors model.SyncResult.Summary in Go.
export function summarizeResult(r: SyncResult): string {
  let s = `uploaded ${r.uploaded}, downloaded ${r.downloaded}, deleted in cloud ${r.deletedRemote}, deleted locally ${r.deletedLocal}, adopted ${r.adopted}, conflicts ${r.conflicts?.length ?? 0}`
  if (r.deferred?.length) s += `, deferred ${r.deferred.length}`
  if (r.pendingDeletes?.length) s += `, pending deletions ${r.pendingDeletes.length}`
  if (r.unsupported?.length) s += `, unsupported ${r.unsupported.length}`
  return s
}
