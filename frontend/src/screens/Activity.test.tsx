import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
import App from '../App'

const tr = { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }

function boot() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v1', agent: true, actions: [
    { action: { id: 'a1', name: 'Editor', items: [], machines: {}, updatedAt: 't', triggers: tr }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 },
  ] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
}

const goActivity = () => fireEvent.click(screen.getByRole('button', { name: 'Activity' }))

test('activity lists results and filters by action', async () => {
  boot()
  await screen.findByText('Editor')
  act(() => emit('agent:event', { type: 'action.result', actionId: 'a1', error: 'network is down', time: '2026-10-04T01:00:00Z' }))
  goActivity()
  expect(await screen.findByText('network is down')).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Filter by action'), { target: { value: '__other__' } })
  expect(screen.queryByText('network is down')).toBeNull()
  fireEvent.change(screen.getByLabelText('Filter by action'), { target: { value: 'a1' } })
  expect(screen.getByText('network is down')).toBeInTheDocument()
})

test('Other selects entries with no action or a removed action', async () => {
  boot()
  await screen.findByText('Editor')
  act(() => {
    emit('agent:event', { type: 'action.result', actionId: 'a1', error: 'known failure', time: '2026-10-04T01:00:00Z' })
    emit('agent:event', { type: 'action.result', actionId: 'gone', error: 'orphan failure', time: '2026-10-04T01:00:01Z' })
    emit('agent:event', { type: 'action.result', error: 'no action failure', time: '2026-10-04T01:00:02Z' })
  })
  goActivity()
  fireEvent.change(await screen.findByLabelText('Filter by action'), { target: { value: '__other__' } })
  expect(screen.getByText('orphan failure')).toBeInTheDocument()
  expect(screen.getByText('no action failure')).toBeInTheDocument()
  expect(screen.queryByText('known failure')).toBeNull()
  fireEvent.change(screen.getByLabelText('Filter by action'), { target: { value: '' } })
  expect(screen.getAllByRole('row')).toHaveLength(4)
})

test('rows show a local time and a text kind label for errors', async () => {
  boot()
  await screen.findByText('Editor')
  const t = '2026-10-04T01:00:00Z'
  act(() => {
    emit('agent:event', { type: 'action.result', actionId: 'a1', error: 'boom', time: t })
    emit('agent:event', { type: 'action.result', actionId: 'a1', result: { outcome: 'ok', uploaded: 1, downloaded: 0, deletedRemote: 0, deletedLocal: 0, adopted: 0, conflicts: [] }, time: '2026-10-04T02:00:00Z' })
  })
  goActivity()
  const row = (await screen.findByText('boom')).closest('tr')!
  expect(within(row).getByText(new Date(t).toLocaleString())).toBeInTheDocument()
  expect(within(row).getByText('error')).toBeInTheDocument()
  expect(screen.getByText('result')).toBeInTheDocument()
})

test('empty state, and a failing Open log folder shows a toast', async () => {
  boot()
  await screen.findByText('Editor')
  goActivity()
  expect(await screen.findByText('Nothing yet')).toBeInTheDocument()
  bindings.OpenLogFolder.mockRejectedValueOnce('{"code":"internal","message":"no finder"}')
  fireEvent.click(screen.getByText('Open log folder'))
  expect(await screen.findByText('Something went wrong.')).toBeInTheDocument()
})
