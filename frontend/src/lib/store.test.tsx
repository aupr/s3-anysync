import { vi } from 'vitest'
vi.mock('./bridge', () => import('../test/mockBridge'))
import { act, render, screen, waitFor } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
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
  return <div>{state.connection.connected ? `on:${Object.keys(state.actions).join(',')}` : 'off'}</div>
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
  bindings.Action.mockResolvedValue({ ...st('a', 'idle'), lastOutcome: 'ok' })
  act(() => emit('agent:event', { type: 'action.result', actionId: 'a', result: null, time: 't' }))
  await waitFor(() => expect(bindings.Action).toHaveBeenCalledWith('a'))
})
