import { vi } from 'vitest'
vi.mock('./bridge', () => import('../test/mockBridge'))
import { act, render, screen } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
import { api } from './api'
import { ACTIVITY_LIMIT, initialState, reducer, StoreProvider, useStore } from './store'
import type { ActionStatus } from './types'

const st = (id: string, state: ActionStatus['state'], name = 'Editor'): ActionStatus =>
  ({ action: { id, name } as ActionStatus['action'], profileId: 'p', state, running: false, failures: 0, conflicts: 0, pendingDeletes: 0 })

test('state events update the action and log only real state changes', () => {
  let s = reducer(initialState, { type: 'actions', list: [st('a', 'idle')] })
  s = reducer(s, { type: 'event', event: { type: 'action.state', actionId: 'a', status: { ...st('a', 'idle'), running: true }, time: 't1' } })
  expect(s.actions.a.running).toBe(true)
  expect(s.activity).toHaveLength(0)
  s = reducer(s, { type: 'event', event: { type: 'action.state', actionId: 'a', status: st('a', 'paused-conflict'), time: 't2' } })
  expect(s.activity[0]).toMatchObject({ kind: 'state', actionName: 'Editor', text: 'Idle → Conflicts' })
})

test('result events are logged newest first and capped', () => {
  let s = reducer(initialState, { type: 'actions', list: [st('a', 'idle')] })
  const result = { outcome: 'ok', uploaded: 2, downloaded: 0, deletedRemote: 0, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null }
  for (let i = 0; i < ACTIVITY_LIMIT + 5; i++) {
    s = reducer(s, { type: 'event', event: { type: 'action.result', actionId: 'a', result, time: `t${i}` } })
  }
  expect(s.activity).toHaveLength(ACTIVITY_LIMIT)
  expect(s.activity[0].time).toBe(`t${ACTIVITY_LIMIT + 4}`)
  expect(s.activity[0].text).toBe('ok: uploaded 2, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 0')
  s = reducer(s, { type: 'event', event: { type: 'action.result', actionId: 'a', error: 'network down', time: 'tx' } })
  expect(s.activity[0]).toMatchObject({ kind: 'error', text: 'network down' })
})

test('actions list replaces the map (left actions disappear)', () => {
  let s = reducer(initialState, { type: 'actions', list: [st('a', 'idle'), st('b', 'idle')] })
  s = reducer(s, { type: 'actions', list: [st('b', 'idle')] })
  expect(Object.keys(s.actions)).toEqual(['b'])
})

function Probe() {
  const { state } = useStore()
  return <div><div>{state.connection.connected ? `on:${Object.keys(state.actions).join(',')}` : 'off'}</div><span>{state.actions.a?.lastOutcome ? `outcome:${state.actions.a.lastOutcome}` : ''}</span></div>
}

test('provider loads everything on connect and refetches on events', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: false, reason: 'connecting' })
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'mac', version: 'v', agent: true, actions: [st('a', 'idle')] })
  bindings.Profiles.mockResolvedValue([])
  render(<StoreProvider><Probe /></StoreProvider>)
  expect(await screen.findByText('off')).toBeInTheDocument()
  act(() => emit('agent:connection', { connected: true }))
  expect(await screen.findByText('on:a')).toBeInTheDocument()
  bindings.Actions.mockResolvedValue([st('a', 'idle'), st('b', 'idle')])
  act(() => emit('agent:event', { type: 'actions.changed', actionId: 'b', time: 't' }))
  expect(await screen.findByText('on:a,b')).toBeInTheDocument()
  bindings.Action.mockClear()
  act(() => emit('agent:event', { type: 'action.result', actionId: 'a', result: null, time: 't' }))
  act(() => emit('agent:event', { type: 'action.state', actionId: 'a', status: { ...st('a', 'idle'), lastOutcome: 'ok' }, time: 't' }))
  expect(await screen.findByText('outcome:ok')).toBeInTheDocument()
  expect(bindings.Action).not.toHaveBeenCalled()
})

function Probe2() {
  const { state } = useStore()
  return <div>{`${state.connection.connected ? 'on' : 'off'}|${state.actions.a?.running ? 'running' : 'idle'}|${state.actions.a?.lastOutcome ?? ''}|${state.actions.b ? 'b' : ''}`}</div>
}

test('a state event during an Actions() refetch survives the stale response', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'mac', version: 'v', agent: true, actions: [st('a', 'idle')] })
  bindings.Profiles.mockResolvedValue([])
  render(<StoreProvider><Probe2 /></StoreProvider>)
  expect(await screen.findByText('on|idle||')).toBeInTheDocument()
  let resolve!: (l: ActionStatus[]) => void
  bindings.Actions.mockReturnValue(new Promise((r) => { resolve = r }))
  act(() => emit('agent:event', { type: 'actions.changed', actionId: 'b', time: 't' }))
  act(() => emit('agent:event', { type: 'action.state', actionId: 'a', status: { ...st('a', 'idle'), running: true }, time: 't' }))
  expect(await screen.findByText('on|running||')).toBeInTheDocument()
  await act(async () => { resolve([st('a', 'idle'), st('b', 'idle')]) })
  expect(await screen.findByText('on|running||b')).toBeInTheDocument()
})

test('a late ConnectionState result does not override an agent:connection event', async () => {
  let resolve!: (c: unknown) => void
  bindings.ConnectionState.mockReturnValue(new Promise((r) => { resolve = r }))
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  render(<StoreProvider><Probe2 /></StoreProvider>)
  act(() => emit('agent:connection', { connected: true }))
  expect(await screen.findByText(/^on\|/)).toBeInTheDocument()
  await act(async () => { resolve({ connected: false, reason: 'stale' }) })
  expect(screen.getByText(/^on\|/)).toBeInTheDocument()
})

test('the status reducer keeps listed actions', () => {
  let s = reducer(initialState, { type: 'actions', list: [{ ...st('a', 'idle'), running: true }] })
  s = reducer(s, { type: 'status', status: { actions: [st('a', 'idle')] } as never, keep: ['a'] })
  expect(s.actions.a.running).toBe(true)
})

test('a not-found rejection refetches status and profiles once', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'mac', version: 'v', agent: true, actions: [st('a', 'idle')] })
  bindings.Profiles.mockResolvedValue([])
  bindings.Pause.mockRejectedValue('{"code":"not-found","message":"gone"}')
  render(<StoreProvider><Probe /></StoreProvider>)
  expect(await screen.findByText('on:a')).toBeInTheDocument()
  bindings.Status.mockClear()
  bindings.Profiles.mockClear()
  await Promise.allSettled([api.pause('a'), api.pause('a')])
  await new Promise((r) => setTimeout(r, 700))
  expect(bindings.Status).toHaveBeenCalledTimes(1)
  expect(bindings.Profiles).toHaveBeenCalledTimes(1)
})
