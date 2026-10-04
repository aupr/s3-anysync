import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'

const f = (path: string) => ({ path, hash: 'abcdef0123', size: 12, mtime: 1_700_000_000_000_000_000 })
const status = (state: string, extra = {}) => ({
  action: { id: 'a1', name: 'Editor', items: [], machines: { m1: 'mac' }, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } },
  profileId: 'p1', state, running: false, failures: 0, conflicts: 0, pendingDeletes: 0, ...extra,
})
const okResult = { outcome: 'ok', uploaded: 1, downloaded: 0, deletedRemote: 1, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null }
const detail = (extra = {}) => ({ conflict: {}, localText: 'x\n', remoteText: 'y\n', binary: false, tooLarge: false, ...extra })
const flush = () => act(async () => { await Promise.resolve() })
const deferred = <T,>() => {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const two = [
  { path: 'a.json', local: f('a.json'), remote: f('a.json'), detectedAt: 't' },
  { path: 'gone.json', local: null, remote: f('gone.json'), detectedAt: 't' },
]

function boot(st: unknown) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [st] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
}
async function openConflicts(list: unknown = two, n = 2) {
  const empty = Array.isArray(list) && list.length === 0
  bindings.Conflicts.mockResolvedValue(list)
  bindings.ConflictDetail.mockResolvedValue(detail())
  boot(status('paused-conflict', { conflicts: n }))
  fireEvent.click(await screen.findByText(`${n} conflicts`))
  if (!empty) await screen.findByText('Conflicts in Editor')
}

test('apply needs every choice and warns about deletions before resolving', async () => {
  await openConflicts()
  const apply = await screen.findByRole('button', { name: 'Apply' })
  expect(apply).toBeDisabled()
  fireEvent.click(screen.getByText('Keep all local'))
  expect(apply).toBeEnabled()
  fireEvent.click(apply)
  const dlg = await screen.findByRole('dialog', { name: 'Some files will be deleted' })
  expect(within(dlg).getByText(/gone.json/)).toBeInTheDocument()
  expect(within(dlg).getByText(/deleted in the cloud/)).toBeInTheDocument()
  bindings.Resolve.mockResolvedValue(okResult)
  fireEvent.click(within(dlg).getByText('Resolve'))
  await waitFor(() => expect(bindings.Resolve).toHaveBeenCalledWith('a1', { 'a.json': 'local', 'gone.json': 'local' }))
  expect(await screen.findByText(/Resolved: uploaded 1/)).toBeInTheDocument()
})

test('per-file radios have unique names and keep-cloud of a file that was deleted on the cloud side deletes it here', async () => {
  const list = [{ path: 'c.json', local: f('c.json'), remote: null, detectedAt: 't' }, two[0]]
  await openConflicts(list)
  fireEvent.click(screen.getByLabelText('Keep cloud c.json'))
  fireEvent.click(screen.getByLabelText('Keep local a.json'))
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  const dlg = await screen.findByRole('dialog', { name: 'Some files will be deleted' })
  expect(within(dlg).getByText(/c.json.*deleted on this machine/)).toBeInTheDocument()
  expect(within(dlg).getByText(/local backup/)).toBeInTheDocument()
})

test('without deletions Apply resolves directly and a failure keeps the choices', async () => {
  await openConflicts([two[0]], 1)
  fireEvent.click(screen.getByLabelText('Keep cloud a.json'))
  bindings.Resolve.mockRejectedValueOnce(JSON.stringify({ code: 'locked', message: 'busy' }))
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByText('Another machine is syncing this action. Try again in a minute.')).toBeInTheDocument()
  expect(bindings.Resolve).toHaveBeenCalledWith('a1', { 'a.json': 'cloud' })
  expect(screen.getByLabelText('Keep cloud a.json')).toBeChecked()
  expect(screen.getByRole('button', { name: 'Apply' })).toBeEnabled()
})

test('double submit is prevented while resolving and the dialog cannot be dismissed', async () => {
  await openConflicts()
  fireEvent.click(screen.getByText('Keep all local'))
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  const dlg = await screen.findByRole('dialog')
  const d = deferred<unknown>()
  bindings.Resolve.mockReturnValue(d.promise)
  fireEvent.click(within(dlg).getByText('Resolve'))
  await flush()
  fireEvent.click(within(dlg).getByText('Resolve'))
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(within(dlg).getByText('Cancel')).toBeDisabled()
  expect(bindings.Resolve).toHaveBeenCalledTimes(1)
  await act(async () => { d.resolve(okResult) })
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(await screen.findByText(/Resolved: uploaded 1/)).toBeInTheDocument()
})

test('stale saved conflicts (invalid) explain and reload the list, dropping the choices', async () => {
  await openConflicts([two[0]], 1)
  fireEvent.click(screen.getByLabelText('Keep local a.json'))
  bindings.Resolve.mockRejectedValueOnce(JSON.stringify({ code: 'invalid', message: 'choose a side for every conflict; missing: n.json' }))
  bindings.Conflicts.mockResolvedValue([two[0], { path: 'n.json', local: f('n.json'), remote: f('n.json'), detectedAt: 't' }])
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByText(/conflicts changed since you looked/)).toBeInTheDocument()
  expect(await screen.findByText('n.json')).toBeInTheDocument()
  expect(screen.getByLabelText('Keep local a.json')).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
})

test('a new conflict outcome reloads the list', async () => {
  await openConflicts([two[0]], 1)
  fireEvent.click(screen.getByLabelText('Keep local a.json'))
  bindings.Resolve.mockResolvedValueOnce({ ...okResult, outcome: 'conflict', conflicts: [two[0]] })
  bindings.Conflicts.mockClear()
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByText(/New conflicts appeared/)).toBeInTheDocument()
  await waitFor(() => expect(bindings.Conflicts).toHaveBeenCalledTimes(1))
})

test('deferred files stay in the list; held-back deletions lead to the review screen', async () => {
  await openConflicts([two[0]], 1)
  fireEvent.click(screen.getByLabelText('Keep local a.json'))
  bindings.Resolve.mockResolvedValueOnce({ ...okResult, deferred: ['a.json'] })
  bindings.Conflicts.mockClear()
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByText(/Resolved, but 1 file\(s\) changed meanwhile.*deferred 1/)).toBeInTheDocument()
  await waitFor(() => expect(bindings.Conflicts).toHaveBeenCalledTimes(1))
  expect(screen.getByText('Conflicts in Editor')).toBeInTheDocument()
  fireEvent.click(screen.getByLabelText('Keep local a.json'))
  bindings.Resolve.mockResolvedValueOnce({ ...okResult, outcome: 'deletes-pending', pendingDeletes: ['z'] })
  bindings.PendingDeletes.mockResolvedValue({ paths: ['z'], approved: false, detectedAt: 't' })
  fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByText(/held back by the delete guard/)).toBeInTheDocument()
  expect(await screen.findByText('Deletions held back in Editor')).toBeInTheDocument()
})

test('the list failing to load shows a message with Retry', async () => {
  bindings.Conflicts.mockRejectedValueOnce(JSON.stringify({ code: 'busy', message: 'b' }))
  boot(status('paused-conflict', { conflicts: 2 }))
  fireEvent.click(await screen.findByText('2 conflicts'))
  expect(await screen.findByText(/Another s3-anysync process is using the data folder/)).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  bindings.Conflicts.mockResolvedValueOnce(two)
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('gone.json')).toBeInTheDocument()
})

test('an empty list says there are no conflicts', async () => {
  await openConflicts([], 1)
  expect(await screen.findByText('No conflicts')).toBeInTheDocument()
})

test('opening a file shows its diff; a binary or too large file shows a note', async () => {
  await openConflicts()
  bindings.ConflictDetail.mockResolvedValueOnce(detail({ localText: 'old\n', remoteText: 'new\n' }))
  fireEvent.click(screen.getByRole('button', { name: 'a.json' }))
  expect(await screen.findByText('old')).toBeInTheDocument()
  expect(bindings.ConflictDetail).toHaveBeenCalledWith('a1', 'a.json')
  bindings.ConflictDetail.mockResolvedValueOnce(detail({ binary: true }))
  fireEvent.click(screen.getByRole('button', { name: 'gone.json' }))
  expect(await screen.findByText(/Binary file/)).toBeInTheDocument()
  expect(screen.queryByText('old')).not.toBeInTheDocument()
  bindings.ConflictDetail.mockResolvedValueOnce(detail({ tooLarge: true }))
  fireEvent.click(screen.getByRole('button', { name: 'a.json' }))
  expect(await screen.findByText(/Too large to compare/)).toBeInTheDocument()
})

test('a slow detail of file A never shows under file B', async () => {
  await openConflicts()
  const a = deferred<unknown>()
  bindings.ConflictDetail.mockReturnValueOnce(a.promise)
  fireEvent.click(screen.getByRole('button', { name: 'a.json' }))
  bindings.ConflictDetail.mockResolvedValueOnce(detail({ localText: 'from-b\n', remoteText: 'from-b2\n' }))
  fireEvent.click(screen.getByRole('button', { name: 'gone.json' }))
  expect(await screen.findByText('from-b')).toBeInTheDocument()
  await act(async () => { a.resolve(detail({ localText: 'from-a\n', remoteText: 'from-a2\n' })) })
  expect(screen.queryByText('from-a')).not.toBeInTheDocument()
  expect(screen.getByText('from-b')).toBeInTheDocument()
})

test('a failing detail is explained inline with a way to reload the list', async () => {
  await openConflicts()
  bindings.ConflictDetail.mockRejectedValueOnce(JSON.stringify({ code: 'internal', message: 'a.json: the cloud copy changed since the conflict was detected; sync again' }))
  fireEvent.click(screen.getByRole('button', { name: 'a.json' }))
  const msg = await screen.findByText(/the cloud copy changed since the conflict was detected/)
  expect(msg).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  bindings.Conflicts.mockClear()
  fireEvent.click(screen.getByRole('button', { name: 'Reload conflicts' }))
  await waitFor(() => expect(bindings.Conflicts).toHaveBeenCalledTimes(1))
})
