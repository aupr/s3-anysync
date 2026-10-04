import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../../test/mockBridge'
import App from '../../App'
import { formatTime } from '../../lib/format'

const snap = { ts: '20261004T010203Z', reason: 'auto-pre-sync', machineId: 'm1', createdAt: '2026-10-04T01:02:03Z', files: [{ path: 'snippets/a.json', hash: 'h', size: 10, mtime: 0 }, { path: 'snippets/b.json', hash: 'h', size: 20, mtime: 0 }] }
const restoreName = (s: { createdAt: string }) => `Restore snapshot ${formatTime(s.createdAt)}`

const flush = () => act(async () => { await Promise.resolve() })
const deferred = <T,>() => {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function openSnapshots() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [
    { action: { id: 'a1', name: 'Editor', items: [], machines: { m1: 'mac' }, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 },
  ] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Snapshots' }))
}

async function openRestore(s = snap) {
  fireEvent.click(await screen.findByRole('button', { name: restoreName(s) }))
  return screen.getByRole('dialog')
}

test('restore selected files to the chosen target', async () => {
  bindings.Snapshots.mockResolvedValue([snap])
  await openSnapshots()
  expect(await screen.findByText('Before a sync')).toBeInTheDocument()
  const dlg = await openRestore()
  fireEvent.click(within(dlg).getByLabelText('Selected files only'))
  fireEvent.click(within(dlg).getByLabelText('snippets/b.json'))
  fireEvent.click(within(dlg).getByLabelText('This machine only'))
  bindings.Restore.mockResolvedValue({ restored: ['snippets/b.json'], skippedUnmapped: null })
  fireEvent.click(within(dlg).getByRole('button', { name: 'Restore' }))
  await waitFor(() => expect(bindings.Restore).toHaveBeenCalledWith('a1', { ts: '20261004T010203Z', paths: ['snippets/b.json'], to: 'local' }))
  expect(await screen.findByText('Restored 1 file(s).')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

test('restoring everything sends no paths; "selected only" with nothing selected cannot be submitted', async () => {
  bindings.Snapshots.mockResolvedValue([snap])
  await openSnapshots()
  const dlg = await openRestore()
  fireEvent.click(within(dlg).getByLabelText('Selected files only'))
  expect(within(dlg).getByRole('button', { name: 'Restore' })).toBeDisabled()
  fireEvent.click(within(dlg).getByLabelText('All 2 files'))
  bindings.Restore.mockResolvedValue({ restored: ['snippets/a.json', 'snippets/b.json'], skippedUnmapped: ['x/y'] })
  fireEvent.click(within(dlg).getByRole('button', { name: 'Restore' }))
  await waitFor(() => expect(bindings.Restore).toHaveBeenCalledWith('a1', { ts: snap.ts, paths: undefined, to: 'both' }))
  expect(await screen.findByText(/Restored 2 file\(s\)\. 1 file\(s\) belong to items not mapped here/)).toBeInTheDocument()
})

test('a snapshot without files cannot be restored', async () => {
  bindings.Snapshots.mockResolvedValue([{ ...snap, files: null }])
  await openSnapshots()
  const dlg = await openRestore()
  expect(within(dlg).getByRole('button', { name: 'Restore' })).toBeDisabled()
})

test('while Restore runs the dialog cannot be closed, a second click is ignored, and a failure leaves it open to retry', async () => {
  bindings.Snapshots.mockResolvedValue([snap])
  await openSnapshots()
  const dlg = await openRestore()
  const req = deferred<unknown>()
  bindings.Restore.mockReturnValue(req.promise)
  const go = within(dlg).getByRole('button', { name: 'Restore' })
  fireEvent.click(go)
  fireEvent.click(go)
  await flush()
  expect(bindings.Restore).toHaveBeenCalledTimes(1)
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  await act(async () => { req.reject('{"code":"locked","message":"held"}') })
  expect(await screen.findByText(/Another machine is syncing/)).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Restore' })).toBeEnabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a cancelled restore says so', async () => {
  bindings.Snapshots.mockResolvedValue([snap])
  await openSnapshots()
  const dlg = await openRestore()
  bindings.Restore.mockRejectedValue('{"code":"cancelled","message":"x"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Restore' }))
  expect(await screen.findByText('Restore cancelled.')).toBeInTheDocument()
})

test('a large snapshot shows the first 500 matches with a hint, and "select all matching" selects every match', async () => {
  const files = Array.from({ length: 1200 }, (_, i) => ({ path: `big/f${String(i).padStart(4, '0')}.txt`, hash: 'h', size: 1, mtime: 0 }))
  bindings.Snapshots.mockResolvedValue([{ ...snap, files }])
  await openSnapshots()
  const dlg = await openRestore()
  fireEvent.click(within(dlg).getByLabelText('Selected files only'))
  expect(within(dlg).getAllByRole('checkbox')).toHaveLength(500)
  expect(within(dlg).getByText(/700 more/)).toBeInTheDocument()
  fireEvent.change(within(dlg).getByLabelText('Filter files'), { target: { value: 'f01' } })
  expect(within(dlg).getAllByRole('checkbox')).toHaveLength(100)
  expect(within(dlg).queryByText(/more/)).not.toBeInTheDocument()
  fireEvent.click(within(dlg).getByRole('button', { name: 'Select all matching' }))
  expect(within(dlg).getByText('100 selected')).toBeInTheDocument()
  bindings.Restore.mockResolvedValue({ restored: [], skippedUnmapped: null })
  fireEvent.click(within(dlg).getByRole('button', { name: 'Restore' }))
  await waitFor(() => expect(bindings.Restore).toHaveBeenCalled())
  const req = bindings.Restore.mock.calls[0][1]
  expect(req.paths).toHaveLength(100)
  expect(req.paths[0]).toBe('big/f0100.txt')
})

test('a failing list shows the problem and Retry reloads it', async () => {
  bindings.Snapshots.mockRejectedValueOnce('{"code":"access-denied","message":"nope"}')
  bindings.Snapshots.mockResolvedValue([snap])
  await openSnapshots()
  expect(await screen.findByText(/bucket refused access/)).toBeInTheDocument()
  expect(screen.queryByRole('status', { name: 'Loading' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('Before a sync')).toBeInTheDocument()
  expect(screen.queryByText(/bucket refused access/)).not.toBeInTheDocument()
})

test('an older list response that arrives last does not replace a newer one', async () => {
  const first = deferred<unknown[]>()
  bindings.Snapshots.mockReturnValueOnce(first.promise)
  bindings.Snapshots.mockResolvedValue([{ ...snap, reason: 'manual' }])
  bindings.Snapshot.mockResolvedValue({ ts: '20261004T010204Z' })
  await openSnapshots()
  fireEvent.click(screen.getByRole('button', { name: 'Snapshot now' }))
  expect(await screen.findByText('Manual')).toBeInTheDocument()
  await act(async () => { first.resolve([]) })
  expect(screen.getByText('Manual')).toBeInTheDocument()
})

test('Snapshot now is disabled while it runs, so a second click does nothing', async () => {
  bindings.Snapshots.mockResolvedValue([])
  await openSnapshots()
  await screen.findByText('No snapshots yet')
  const req = deferred<unknown>()
  bindings.Snapshot.mockReturnValue(req.promise)
  const btn = screen.getByRole('button', { name: 'Snapshot now' })
  fireEvent.click(btn)
  fireEvent.click(btn)
  await flush()
  expect(bindings.Snapshot).toHaveBeenCalledTimes(1)
  expect(btn).toBeDisabled()
  await act(async () => { req.resolve({ ts: '20261004T010204Z' }) })
  await waitFor(() => expect(btn).toBeEnabled())
})

test('lists newest first with every reason label and the machine name', async () => {
  bindings.Snapshots.mockResolvedValue([
    { ...snap, ts: '20261004T010101Z', createdAt: '2026-10-04T01:01:01Z', reason: 'manual' },
    { ...snap, ts: '20261004T010303Z', createdAt: '2026-10-04T01:03:03Z', reason: 'pre-restore', deletes: ['a', 'b'] },
    { ...snap, ts: '20261004T010202Z', createdAt: '2026-10-04T01:02:02Z', reason: 'pre-resolve', machineId: 'mX' },
  ])
  await openSnapshots()
  await screen.findByText('Before a restore')
  const rows = screen.getAllByRole('row').slice(1)
  expect(rows[0]).toHaveTextContent('Before a restore')
  expect(rows[0]).toHaveTextContent('before deleting 2 file(s)')
  expect(rows[0]).toHaveTextContent('mac')
  expect(rows[1]).toHaveTextContent('Before resolving conflicts')
  expect(rows[1]).toHaveTextContent('mX')
  expect(rows[2]).toHaveTextContent('Manual')
})
