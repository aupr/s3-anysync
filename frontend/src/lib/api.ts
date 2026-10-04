import { bindings } from './bridge'
import type {
  Action, ActionStatus, ConflictDetail, Conflict, ConflictVersion, ConnState, CreateActionRequest, Direction, HistoryEntry, Item,
  JoinActionRequest, NewItem, PendingDeletes, ProbeResult, Profile, RestoreRequest, RestoreResult, Rules, Schedule,
  Settings, Side, SnapshotInfo, Status, SyncResult,
} from './types'

export class AgentError extends Error {
  constructor(public code: string, message: string) {
    super(message)
    this.name = 'AgentError'
  }
}

// toAgentError turns a rejected binding call into an AgentError. Go errors
// arrive as the JSON {"code","message"} (internal/gui/errors.go).
export function toAgentError(e: unknown): AgentError {
  if (e instanceof AgentError) return e
  const raw = typeof e === 'string' ? e : e instanceof Error ? e.message : String(e)
  try {
    const j = JSON.parse(raw)
    if (j && typeof j.code === 'string') return new AgentError(j.code, String(j.message ?? ''))
  } catch {
    // not JSON
  }
  return new AgentError('internal', raw)
}

export const NOT_FOUND_EVENT = 's3as:not-found'

function call<T>(name: string, ...args: unknown[]): Promise<T> {
  return Promise.resolve().then(() => bindings[name](...args) as Promise<T>).catch((e: unknown) => {
    const err = toAgentError(e)
    // The store listens and refetches: the thing the user acted on is gone.
    if (err.code === 'not-found' && typeof window !== 'undefined') window.dispatchEvent(new CustomEvent(NOT_FOUND_EVENT))
    throw err
  })
}

const orEmpty = <T,>(v: T[] | null | undefined): T[] => v ?? []

export const api = {
  status: () => call<Status>('Status'),
  settings: () => call<Settings>('Settings'),
  updateSettings: (s: Settings) => call<Settings>('UpdateSettings', s),
  profiles: () => call<Profile[] | null>('Profiles').then(orEmpty),
  addProfile: (profile: Profile, secret: string) => call<Profile>('AddProfile', { profile, secret }),
  updateProfile: (profile: Profile, secret: string) => call<Profile>('UpdateProfile', { profile, secret }),
  removeProfile: (id: string) => call<void>('RemoveProfile', id),
  testProfile: (id: string) => call<ProbeResult>('TestProfile', id),
  discover: (profileId: string) => call<Action[] | null>('Discover', profileId).then(orEmpty),
  actions: () => call<ActionStatus[] | null>('Actions').then(orEmpty),
  action: (id: string) => call<ActionStatus>('Action', id),
  createAction: (req: CreateActionRequest) => call<Action>('CreateAction', req),
  joinAction: (req: JoinActionRequest) => call<Action>('JoinAction', req),
  updateRules: (id: string, rules: Rules, expectedUpdatedAt: string) =>
    call<Action>('UpdateRules', id, { rules, expectedUpdatedAt }),
  // '' resets this machine to the action's default direction.
  setMachineDirection: (id: string, d: Direction | '') => call<void>('SetMachineDirection', id, d),
  leaveAction: (id: string, force: boolean) => call<void>('LeaveAction', id, force),
  addItem: (id: string, item: NewItem, localPath: string) => call<Item>('AddItem', id, { item, localPath }),
  mapItem: (id: string, itemId: string, localPath: string) => call<void>('MapItem', id, itemId, localPath),
  unmapItem: (id: string, itemId: string) => call<void>('UnmapItem', id, itemId),
  removeItem: (id: string, itemId: string) => call<void>('RemoveItem', id, itemId),
  sync: (id: string) => call<SyncResult>('Sync', id),
  cancelSync: (id: string) => call<void>('CancelSync', id),
  snapshot: (id: string) => call<SnapshotInfo>('Snapshot', id),
  pause: (id: string) => call<void>('Pause', id),
  resume: (id: string) => call<void>('Resume', id),
  pendingDeletes: (id: string) => call<PendingDeletes | null>('PendingDeletes', id),
  approveDeletes: (id: string, reviewed: string[]) => call<SyncResult>('ApproveDeletes', id, reviewed),
  snapshots: (id: string) => call<SnapshotInfo[] | null>('Snapshots', id).then(orEmpty),
  restore: (id: string, req: RestoreRequest) => call<RestoreResult>('Restore', id, req),
  conflicts: (id: string) => call<Conflict[] | null>('Conflicts', id).then(orEmpty),
  conflictDetail: (id: string, path: string) => call<ConflictDetail>('ConflictDetail', id, path),
  resolve: (id: string, choices: Record<string, Side>, reviewed: Record<string, ConflictVersion>) => call<SyncResult>('Resolve', id, { choices, reviewed }),
  history: (id: string, limit: number) => call<HistoryEntry[] | null>('History', id, limit).then(orEmpty),
  previewSchedule: (s: Schedule) => call<string[] | null>('PreviewSchedule', s).then(orEmpty),
  pickFile: (title: string) => call<string>('PickFile', title),
  pickFolder: (title: string) => call<string>('PickFolder', title),
  openDataFolder: () => call<void>('OpenDataFolder'),
  openLogFolder: () => call<void>('OpenLogFolder'),
  connectionState: () => call<ConnState>('ConnectionState'),
  retryConnect: () => call<void>('RetryConnect'),
  dataDir: () => call<string>('DataDir'),
}
