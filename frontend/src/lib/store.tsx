import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, type Dispatch, type ReactNode } from 'react'
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
  | { type: 'status'; status: Status }
  | { type: 'actions'; list: ActionStatus[] }
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

const byId = (list: ActionStatus[]) => Object.fromEntries(list.map((a) => [a.action.id, a]))

function log(s: State, e: Omit<ActivityEntry, 'id'>): State {
  const entry = { ...e, id: s.seq + 1 }
  return { ...s, seq: entry.id, activity: [entry, ...s.activity].slice(0, ACTIVITY_LIMIT) }
}

export function reducer(s: State, m: Msg): State {
  switch (m.type) {
    case 'connection':
      return { ...s, connection: m.conn }
    case 'status':
      return { ...s, status: m.status, actions: byId(m.status.actions ?? []) }
    case 'actions':
      return { ...s, actions: byId(m.list) }
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
  const reloadProfiles = useCallback(async () => {
    dispatch({ type: 'profiles', list: await api.profiles() })
  }, [])
  const refresh = useCallback(async () => {
    const [status, profiles] = await Promise.all([api.status(), api.profiles()])
    dispatch({ type: 'status', status })
    dispatch({ type: 'profiles', list: profiles })
  }, [])
  useEffect(() => {
    const quiet = () => {} // a failed background refresh shows up as a connection change
    const onConn = (conn: ConnState) => {
      dispatch({ type: 'connection', conn })
      if (conn.connected) refresh().catch(quiet)
    }
    const offConn = onEvent('agent:connection', onConn)
    const offEv = onEvent('agent:event', (ev: AgentEvent) => {
      dispatch({ type: 'event', event: ev })
      if (ev.type === 'action.result' && ev.actionId) {
        api.action(ev.actionId).then((status) => dispatch({ type: 'action', status })).catch(quiet)
      }
      if (ev.type === 'actions.changed') {
        api.actions().then((list) => dispatch({ type: 'actions', list })).catch(quiet)
      }
    })
    api.connectionState().then(onConn).catch(quiet)
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
