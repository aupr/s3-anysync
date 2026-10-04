import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'

const status = (state: string, extra = {}) => ({
  action: { id: 'a1', name: 'Editor', items: [], machines: { m1: 'mac' }, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } },
  profileId: 'p1', state, running: false, failures: 0, conflicts: 0, pendingDeletes: 0, ...extra,
})
const result = (extra = {}) => ({ outcome: 'ok', uploaded: 0, downloaded: 0, deletedRemote: 2, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null, ...extra })
const deferred = <T,>() => {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

async function openReview(paths: string[] | null = ['b', 'a']) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [status('paused-deletes', { pendingDeletes: 2 })] })
  bindings.Profiles.mockResolvedValue([])
  bindings.PendingDeletes.mockResolvedValueOnce(paths && { paths, approved: false, detectedAt: '2026-10-04T01:02:03Z' })
  render(<App />)
  fireEvent.click(await screen.findByText('2 deletions to review'))
}

test('delete review approves exactly the shown list and reloads when it changed', async () => {
  await openReview()
  expect(await screen.findByText('a')).toBeInTheDocument()
  bindings.ApproveDeletes.mockResolvedValueOnce(result({ outcome: 'deletes-pending', deletedRemote: 0, pendingDeletes: ['a', 'b', 'c'] }))
  bindings.PendingDeletes.mockResolvedValueOnce({ paths: ['a', 'b', 'c'], approved: false, detectedAt: 't' })
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  fireEvent.click(within(await screen.findByRole('dialog')).getByText('Approve'))
  await waitFor(() => expect(bindings.ApproveDeletes).toHaveBeenCalledWith('a1', ['a', 'b']))
  expect(await screen.findByText(/nothing was approved/)).toBeInTheDocument()
  expect(await screen.findByText('c')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('approving reports the result and returns to the action', async () => {
  await openReview()
  await screen.findByText('a')
  bindings.ApproveDeletes.mockResolvedValueOnce(result())
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  fireEvent.click(within(await screen.findByRole('dialog')).getByText('Approve'))
  expect(await screen.findByText(/Done: uploaded 0, downloaded 0, deleted in cloud 2/)).toBeInTheDocument()
  expect(await screen.findByText('Direction on this machine')).toBeInTheDocument()
})

test('no double submit and no Escape while approving', async () => {
  await openReview()
  await screen.findByText('a')
  const d = deferred<unknown>()
  bindings.ApproveDeletes.mockReturnValue(d.promise)
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  const dlg = await screen.findByRole('dialog')
  fireEvent.click(within(dlg).getByText('Approve'))
  await act(async () => { await Promise.resolve() })
  fireEvent.click(within(dlg).getByText('Approve'))
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(bindings.ApproveDeletes).toHaveBeenCalledTimes(1)
  await act(async () => { d.resolve(result()) })
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('an approve failure is shown and the list stays', async () => {
  await openReview()
  await screen.findByText('a')
  bindings.ApproveDeletes.mockRejectedValueOnce(JSON.stringify({ code: 'locked', message: 'x' }))
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  fireEvent.click(within(await screen.findByRole('dialog')).getByText('Approve'))
  expect(await screen.findByText('Another machine is syncing this action. Try again in a minute.')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(screen.getByText('Approve 2 deletion(s)')).toBeEnabled()
})

test('nothing pending any more (invalid) reloads to the empty state', async () => {
  await openReview()
  await screen.findByText('a')
  bindings.ApproveDeletes.mockRejectedValueOnce(JSON.stringify({ code: 'invalid', message: 'no deletions are waiting for approval' }))
  bindings.PendingDeletes.mockResolvedValueOnce(null)
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  fireEvent.click(within(await screen.findByRole('dialog')).getByText('Approve'))
  expect(await screen.findByText('Nothing to review')).toBeInTheDocument()
})

test('new conflicts found while approving lead to the resolver', async () => {
  await openReview()
  await screen.findByText('a')
  bindings.ApproveDeletes.mockResolvedValueOnce(result({ outcome: 'conflict', conflicts: [] }))
  bindings.Conflicts.mockResolvedValue([])
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  fireEvent.click(within(await screen.findByRole('dialog')).getByText('Approve'))
  expect(await screen.findByText(/conflicts/i, { selector: '[role="alert"] span' })).toBeInTheDocument()
  expect(await screen.findByText('No conflicts')).toBeInTheDocument()
})

test('a load failure shows a message with Retry; null means nothing to review', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [status('paused-deletes', { pendingDeletes: 2 })] })
  bindings.Profiles.mockResolvedValue([])
  bindings.PendingDeletes.mockRejectedValueOnce(JSON.stringify({ code: 'busy', message: 'b' }))
  render(<App />)
  fireEvent.click(await screen.findByText('2 deletions to review'))
  expect(await screen.findByText(/Another s3-anysync process is using the data folder/)).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  bindings.PendingDeletes.mockResolvedValueOnce(null)
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('Nothing to review')).toBeInTheDocument()
})
