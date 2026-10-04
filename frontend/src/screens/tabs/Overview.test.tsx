import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../../test/mockBridge'
import App from '../../App'
import type { ActionStatus } from '../../lib/types'

const st = (over: Partial<ActionStatus> = {}): ActionStatus => ({
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50, items: [], machines: { m1: 'mac' }, updatedAt: '2026-10-04T01:02:03Z' },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0, ...over,
})

async function open(s: ActionStatus = st()) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [s] })
  bindings.Profiles.mockResolvedValue([])
  const r = render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  await screen.findByText('Leave action')
  return r
}
const deferred = <T,>() => {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const openLeave = () => {
  fireEvent.click(screen.getByRole('button', { name: 'Leave action' }))
  return screen.getByRole('dialog', { name: 'Leave Editor?' })
}

test('shows the direction on this machine and the machines', async () => {
  await open(st({ action: { ...st().action, machineDirections: { m1: 'cloud-to-local' }, machines: { m1: 'mac', m2: 'pc' } } }))
  expect(screen.getByText('Cloud → this machine')).toBeInTheDocument()
  expect(screen.getByText('mac, pc')).toBeInTheDocument()
})

test('banners link to conflicts, deletions and retry', async () => {
  await open(st({ state: 'error', lastError: 'boom', failures: 2 }))
  bindings.Resume.mockResolvedValue(undefined)
  expect(screen.getByText(/boom/)).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Retry now' }))
  await waitFor(() => expect(bindings.Resume).toHaveBeenCalledWith('a1'))
})

test('a conflict banner leads to the conflicts screen', async () => {
  await open(st({ state: 'paused-conflict', conflicts: 3 }))
  expect(screen.getByText(/3 file\(s\) changed on both sides/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Resolve conflicts' })).toBeInTheDocument()
})

test('Sync now runs once even when clicked twice', async () => {
  await open()
  const d = deferred<unknown>()
  bindings.Sync.mockReturnValue(d.promise)
  const b = screen.getByRole('button', { name: 'Sync now' })
  fireEvent.click(b)
  fireEvent.click(b)
  expect(b).toBeDisabled()
  await act(async () => d.resolve({ outcome: 'ok', uploaded: 0, downloaded: 0, deletedRemote: 0, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null }))
  expect(bindings.Sync).toHaveBeenCalledTimes(1)
  await waitFor(() => expect(b).toBeEnabled())
})

test('Pause and Snapshot now call the agent', async () => {
  await open()
  bindings.Snapshot.mockResolvedValue({ ts: 'T1', reason: 'manual', machineId: 'm1', createdAt: 't', files: null })
  fireEvent.click(screen.getByRole('button', { name: 'Snapshot now' }))
  await waitFor(() => expect(bindings.Snapshot).toHaveBeenCalledWith('a1'))
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }))
  await waitFor(() => expect(bindings.Pause).toHaveBeenCalledWith('a1'))
})

test('leave: success returns to the actions list', async () => {
  await open()
  const dlg = openLeave()
  fireEvent.click(within(dlg).getByRole('button', { name: 'Leave' }))
  await waitFor(() => expect(bindings.LeaveAction).toHaveBeenCalledWith('a1', false))
  expect(await screen.findByRole('heading', { name: 'Actions' })).toBeInTheDocument()
})

test('leave: while the request is in flight the dialog cannot be closed or confirmed twice', async () => {
  await open()
  const dlg = openLeave()
  const d = deferred<void>()
  bindings.LeaveAction.mockReturnValue(d.promise)
  const confirm = within(dlg).getByRole('button', { name: 'Leave' })
  fireEvent.click(confirm)
  fireEvent.click(confirm)
  expect(within(dlg).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog', { name: 'Leave Editor?' })).toBeInTheDocument()
  await act(async () => d.resolve())
  expect(bindings.LeaveAction).toHaveBeenCalledTimes(1)
  expect(await screen.findByRole('heading', { name: 'Actions' })).toBeInTheDocument()
})

test.each(['access-denied', 'no-such-bucket', 'not-found', 'passphrase-missing', 'decrypt', 'internal'])(
  'leave: a %s failure offers to leave on this machine only', async (code) => {
    await open()
    const dlg = openLeave()
    bindings.LeaveAction.mockRejectedValueOnce(`{"code":"${code}","message":"cannot update the bucket"}`)
    fireEvent.click(within(dlg).getByRole('button', { name: 'Leave' }))
    const force = await screen.findByRole('dialog', { name: 'Leave anyway?' })
    expect(force).toHaveTextContent('cannot update the bucket')
    fireEvent.click(within(force).getByRole('button', { name: 'Leave on this machine only' }))
    await waitFor(() => expect(bindings.LeaveAction).toHaveBeenLastCalledWith('a1', true))
    expect(await screen.findByRole('heading', { name: 'Actions' })).toBeInTheDocument()
  })

test.each([['locked', /Another machine is syncing/], ['lock-lost', /lost the cloud lock/], ['busy', /Another s3-anysync process/], ['unreachable', /agent is not running/], ['unavailable', /shutting down/], ['cancelled', /^Cancelled\.$/]] as const)(
  'leave: a transient %s failure is a toast without the force option', async (code, toastText) => {
    await open()
    const dlg = openLeave()
    bindings.LeaveAction.mockRejectedValueOnce(`{"code":"${code}","message":"try later"}`)
    fireEvent.click(within(dlg).getByRole('button', { name: 'Leave' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByText('Leave anyway?')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Leave action' })).toBeInTheDocument()
    expect(bindings.LeaveAction).toHaveBeenCalledTimes(1)
    expect(await screen.findByText(toastText)).toBeInTheDocument()
  })

test('leave: a failure while forcing is shown and the dialog closes', async () => {
  await open()
  let dlg = openLeave()
  bindings.LeaveAction.mockRejectedValueOnce('{"code":"access-denied","message":"403"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Leave' }))
  dlg = await screen.findByRole('dialog', { name: 'Leave anyway?' })
  bindings.LeaveAction.mockRejectedValueOnce('{"code":"internal","message":"disk"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Leave on this machine only' }))
  expect(await screen.findByText('Something went wrong.')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('leave: forcing an action that is already gone locally counts as done', async () => {
  await open()
  let dlg = openLeave()
  bindings.LeaveAction.mockRejectedValueOnce('{"code":"internal","message":"passphrase removal failed"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Leave' }))
  dlg = await screen.findByRole('dialog', { name: 'Leave anyway?' })
  bindings.LeaveAction.mockRejectedValueOnce('{"code":"not-found","message":"not found"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Leave on this machine only' }))
  expect(await screen.findByRole('heading', { name: 'Actions' })).toBeInTheDocument()
})

test('leave: a result that arrives after the screen is gone does nothing', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
  const r = await open()
  const dlg = openLeave()
  const d = deferred<void>()
  bindings.LeaveAction.mockReturnValue(d.promise)
  fireEvent.click(within(dlg).getByRole('button', { name: 'Leave' }))
  r.unmount()
  await act(async () => d.reject('{"code":"access-denied","message":"403"}'))
  expect(errors).not.toHaveBeenCalled()
  errors.mockRestore()
})
