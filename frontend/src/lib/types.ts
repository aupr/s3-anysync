// JSON shapes of the Go API (internal/api, internal/model, internal/engine,
// internal/storage, internal/gui). Times are RFC 3339 strings; never turn
// Action.updatedAt into a Date (it is sent back verbatim).
export type Direction = 'two-way' | 'local-to-cloud' | 'cloud-to-local'
export type ScheduleKind = 'none' | 'interval' | 'daily' | 'weekly' | 'cron'
export type Side = 'local' | 'cloud'
export type RestoreTarget = 'local' | 'cloud' | 'both'
export type ActionState = 'idle' | 'running' | 'paused-conflict' | 'paused-deletes' | 'paused' | 'error'

export interface Schedule { kind: ScheduleKind; everyMinutes?: number; at?: string; weekday?: number; cron?: string }
export interface Triggers { onChange: boolean; debounceSeconds: number; schedule: Schedule; remotePollMinutes: number }
export interface Item {
  id: string
  remoteName: string
  type: 'file' | 'dir'
  excludes: string[] | null
  localPaths: Record<string, string> | null
}
export interface Action {
  id: string
  name: string
  direction: Direction
  machineDirections: Record<string, Direction> | null
  triggers: Triggers
  propagateDeletes: boolean
  receiverRestoresCloud: boolean
  encrypt: boolean
  excludes: string[] | null
  snapshotRetention: number
  deleteGuardMinFiles: number
  deleteGuardPercent: number
  items: Item[] | null
  machines: Record<string, string> | null
  updatedAt: string
}
export interface Rules {
  name: string
  direction: Direction
  triggers: Triggers
  propagateDeletes: boolean
  receiverRestoresCloud: boolean
  excludes: string[]
  snapshotRetention: number
  deleteGuardMinFiles: number
  deleteGuardPercent: number
}
export interface ActionStatus {
  action: Action
  profileId: string
  state: ActionState
  running: boolean
  lastRun?: string
  lastOutcome?: string
  lastSummary?: string
  lastError?: string
  failures: number
  nextRun?: string
  conflicts: number
  pendingDeletes: number
}
export interface Status { machineId: string; machineName: string; version: string; agent: boolean; actions: ActionStatus[] | null }
export interface Settings { machineId: string; machineName: string; autostart: boolean; autostartSupported: boolean; logLevel: string }
export interface Profile {
  id: string
  name: string
  provider: string
  endpoint: string
  region: string
  bucket: string
  rootPrefix: string
  accessKeyId: string
  pathStyle: boolean
  conditionalWrites: boolean
}
export interface ProbeResult { list: boolean; put: boolean; get: boolean; delete: boolean; conditionalWrites: boolean }
export interface FileEntry { path: string; hash: string; size: number; mtime: number; etag?: string; writer?: string }
export interface Conflict { path: string; local: FileEntry | null; remote: FileEntry | null; detectedAt: string }
/** The content hashes of a conflict's two sides as the user saw them; '' for a deleted side. */
export interface ConflictVersion { localHash: string; remoteHash: string }
export interface ConflictDetail { conflict: Conflict; localText: string; remoteText: string; binary: boolean; tooLarge: boolean }
export interface SnapshotInfo { ts: string; reason: string; machineId: string; createdAt: string; files: FileEntry[] | null; deletes?: string[] }
export interface SyncResult {
  outcome: string
  uploaded: number
  downloaded: number
  deletedRemote: number
  deletedLocal: number
  adopted: number
  conflicts: Conflict[] | null
  deferred: string[] | null
  pendingDeletes: string[] | null
  unsupported: string[] | null
}
export interface PendingDeletes { paths: string[] | null; approved: boolean; detectedAt: string }
export interface HistoryEntry { id: number; actionId: string; kind: string; startedAt: string; finishedAt: string; outcome: string; message: string }
export interface RestoreResult { restored: string[] | null; skippedUnmapped: string[] | null }
export interface RestoreRequest { ts: string; paths?: string[]; to: RestoreTarget }
export interface NewItem { remoteName: string; type: 'file' | 'dir'; excludes: string[] }
export interface CreateActionRequest { profileId: string; action: Partial<Action> & { name: string }; passphrase?: string }
export interface JoinActionRequest { profileId: string; actionId: string; passphrase?: string }
export interface AgentEvent {
  type: 'action.state' | 'action.result' | 'actions.changed' | 'log'
  actionId?: string
  status?: ActionStatus
  result?: SyncResult
  error?: string
  message?: string
  time: string
}
export interface ConnState { connected: boolean; reason?: string }
