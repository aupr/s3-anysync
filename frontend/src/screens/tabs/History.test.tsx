import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { act, fireEvent, render, screen } from '@testing-library/react'
import { bindings } from '../../test/mockBridge'
import App from '../../App'

const entry = (id: number, over = {}) => ({ id, actionId: 'a1', kind: 'sync', startedAt: '2026-10-04T01:02:03Z', finishedAt: '2026-10-04T01:02:04Z', outcome: 'ok', message: `msg ${id}`, ...over })

async function openHistory() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [
    { action: { id: 'a1', name: 'Editor', items: [], machines: { m1: 'mac' }, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 },
  ] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'History' }))
}

test('shows runs with readable kinds and outcomes', async () => {
  bindings.History.mockResolvedValue([entry(2, { kind: 'approve-deletes', outcome: 'deletes-pending' }), entry(1, { kind: 'restore', outcome: 'error', message: 'boom' })])
  await openHistory()
  expect(await screen.findByText('Approve deletions')).toBeInTheDocument()
  expect(screen.getByText('Deletions waiting')).toBeInTheDocument()
  expect(screen.getByText('Restore')).toBeInTheDocument()
  expect(screen.getByText('boom')).toBeInTheDocument()
  expect(bindings.History).toHaveBeenCalledWith('a1', 100)
})

test('an empty history says nothing has run', async () => {
  bindings.History.mockResolvedValue(null)
  await openHistory()
  expect(await screen.findByText('Nothing has run yet')).toBeInTheDocument()
})

test('a failing load shows the problem, no spinner, and Retry reloads', async () => {
  bindings.History.mockRejectedValueOnce('{"code":"unreachable","message":"x"}')
  bindings.History.mockResolvedValue([entry(1)])
  await openHistory()
  expect(await screen.findByText(/agent is not running/)).toBeInTheDocument()
  expect(screen.queryByRole('status', { name: 'Loading' })).not.toBeInTheDocument()
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Retry' })) })
  expect(await screen.findByText('msg 1')).toBeInTheDocument()
})

test('says when only the newest 100 runs are shown', async () => {
  bindings.History.mockResolvedValue(Array.from({ length: 100 }, (_, i) => entry(i + 1)))
  await openHistory()
  expect(await screen.findByText(/newest 100 runs/)).toBeInTheDocument()
})
