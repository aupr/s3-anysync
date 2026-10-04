import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, useRef, type Dispatch, type ReactNode } from 'react'
import { api } from './api'
import { onEvent } from './bridge'
import { stateLabels, summarizeResult } from './format'
import type { ActionStatus, AgentEvent, ConnState, Profile, Status } from './types'

export const ACTIVITY_LIMIT = 500

export interface ActivityEntry {
  id: number
  time: string
  actionId?: string
  actionName: string
  kind: 'result' | 'state' | 'error'
  text: string
}

export interface State {
  connection: ConnState
  status: Status | null
  actions: Record<string, ActionStatus>
  profiles: Profile[]
  activity: ActivityEntry[]
  seq: number
}

export type Msg =
  | { type: 'connection'; conn: ConnState }
  | { type: 'status'; status: Status; keep?: string[] }
  | { type: 'actions'; list: ActionStatus[]; keep?: string[] }
  | { type: 'action'; status: ActionStatus }
  | { type: 'profiles'; list: Profile[] }
  | { type: 'event'; event: AgentEvent }

export const initialState: State = {
  connection: { connected: false, reason: 'connecting to the agent' },
  status: null,
  actions: {},
  profiles: [],
  activity: [],
  seq: 0,
}

// byId indexes a fetched list; ids in keep retain their current (newer,
// event-sourced) entry from cur.
function byId(list: ActionStatus[], cur: Record<string, ActionStatus>, keep?: string[]) {
  const out = Object.fromEntries(list.map((a) => [a.action.id, a]))
  for (const id of keep ?? []) if (id in out && cur[id]) out[id] = cur[id]
  return out
}

function log(s: State, e: Omit<ActivityEntry, 'id'>): State {
  const entry = { ...e, id: s.seq + 1 }
  return { ...s, seq: entry.id, activity: [entry, ...s.activity].slice(0, ACTIVITY_LIMIT) }
}

export function reducer(s: State, m: Msg): State {
  switch (m.type) {
    case 'connection':
      return { ...s, connection: m.conn }
    case 'status':
      return { ...s, status: m.status, actions: byId(m.status.actions ?? [], s.actions, m.keep) }
    case 'actions':
      return { ...s, actions: byId(m.list, s.actions, m.keep) }
    case 'action':
      return { ...s, actions: { ...s.actions, [m.status.action.id]: m.status } }
    case 'profiles':
      return { ...s, profiles: m.list }
    case 'event': {
      const ev = m.event
      const id = ev.actionId
      const name = (id && (s.actions[id]?.action.name ?? ev.status?.action.name)) || id || ''
      if (ev.type === 'action.state' && id && ev.status) {
        const prev = s.actions[id]
        const next = { ...s, actions: { ...s.actions, [id]: ev.status } }
        if (prev && prev.state !== ev.status.state) {
          return log(next, { time: ev.time, actionId: id, actionName: name, kind: 'state', text: `${stateLabels[prev.state]} → ${stateLabels[ev.status.state]}` })
        }
        return next
      }
      if (ev.type === 'action.result') {
        if (ev.error) return log(s, { time: ev.time, actionId: id, actionName: name, kind: 'error', text: ev.error })
        if (ev.result) {
          return log(s, { time: ev.time, actionId: id, actionName: name, kind: 'result', text: `${ev.result.outcome}: ${summarizeResult(ev.result)}` })
        }
      }
      return s
    }
  }
}

interface StoreValue {
  state: State
  dispatch: Dispatch<Msg>
  refresh: () => Promise<void>
  reloadProfiles: () => Promise<void>
}

const Ctx = createContext<StoreValue | null>(null)

export function StoreProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState)
  // Per action: how many action.state events arrived. A fetched snapshot whose
  // request overlapped an event for that action is older than the event.
  const counters = useRef(new Map<string, number>())
  const changed = (before: Map<string, number>, ids: string[]) =>
    ids.filter((id) => (counters.current.get(id) ?? 0) !== (before.get(id) ?? 0))
  const reloadProfiles = useCallback(async () => {
    dispatch({ type: 'profiles', list: await api.profiles() })
  }, [])
  const refresh = useCallback(async () => {
    const before = new Map(counters.current)
    const [status, profiles] = await Promise.all([api.status(), api.profiles()])
    dispatch({ type: 'status', status, keep: changed(before, (status.actions ?? []).map((a) => a.action.id)) })
    dispatch({ type: 'profiles', list: profiles })
  }, [])
  useEffect(() => {
    const quiet = () => {} // a failed background refresh shows up as a connection change
    let sawConnEvent = false
    const onConn = (conn: ConnState) => {
      dispatch({ type: 'connection', conn })
      if (conn.connected) refresh().catch(quiet)
    }
    const offConn = onEvent('agent:connection', (conn: ConnState) => {
      sawConnEvent = true
      onConn(conn)
    })
    const offEv = onEvent('agent:event', (ev: AgentEvent) => {
      if (ev.type === 'action.state' && ev.actionId) {
        counters.current.set(ev.actionId, (counters.current.get(ev.actionId) ?? 0) + 1)
      }
      dispatch({ type: 'event', event: ev })
      // action.result is always followed by a full action.state event, so it needs no refetch.
      if (ev.type === 'actions.changed') {
        const before = new Map(counters.current)
        api.actions().then((list) => dispatch({ type: 'actions', list, keep: changed(before, list.map((a) => a.action.id)) })).catch(quiet)
      }
    })
    // A connection event that arrived first is newer than this initial answer.
    api.connectionState().then((conn) => { if (!sawConnEvent) onConn(conn) }).catch(quiet)
    return () => {
      offConn()
      offEv()
    }
  }, [refresh])
  const value = useMemo(() => ({ state, dispatch, refresh, reloadProfiles }), [state, refresh, reloadProfiles])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useStore(): StoreValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useStore outside StoreProvider')
  return v
}
