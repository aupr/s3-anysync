import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings, emit } from '../../test/mockBridge'
import App from '../../App'
import type { ActionStatus } from '../../lib/types'

const st: ActionStatus = {
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50,
    items: [
      { id: 'i1', remoteName: 'snippets', type: 'dir', excludes: null, localPaths: { m1: '/Users/me/snippets', m2: 'C:\\Users\\me\\snippets' } },
      { id: 'i2', remoteName: 'settings.json', type: 'file', excludes: null, localPaths: { m2: 'C:\\x\\settings.json' } },
    ],
    machines: { m1: 'mac', m2: 'pc' }, updatedAt: 't' },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0,
}

const flush = () => act(async () => { await Promise.resolve() })
const deferred = <T,>() => {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function openItems() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [st] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Items' }))
}

const openAdd = () => {
  fireEvent.click(screen.getByRole('button', { name: 'Add item' }))
  return screen.getByRole('dialog', { name: 'Add item' })
}

async function chooseFolder(dlg: HTMLElement, path: string) {
  bindings.PickFolder.mockResolvedValue(path)
  fireEvent.click(within(dlg).getByRole('button', { name: 'Choose…' }))
  await waitFor(() => expect(within(dlg).getByText(path)).toBeInTheDocument())
}

test("shows this machine's path, other machines read-only, and maps an unmapped item with the right picker", async () => {
  await openItems()
  const snippets = screen.getByText('snippets').closest('tr')!
  expect(within(snippets).getByText('/Users/me/snippets')).toBeInTheDocument()
  expect(within(snippets).getByText(/pc: C:\\Users\\me\\snippets/)).toBeInTheDocument()
  const settings = screen.getByText('settings.json').closest('tr')!
  expect(within(settings).getByText('Not mapped on this machine')).toBeInTheDocument()
  bindings.PickFile.mockResolvedValue('/Users/me/settings.json')
  fireEvent.click(screen.getByRole('button', { name: 'Map settings.json' }))
  await waitFor(() => expect(bindings.MapItem).toHaveBeenCalledWith('a1', 'i2', '/Users/me/settings.json'))
  expect(bindings.PickFolder).not.toHaveBeenCalled()
})

test('a whitespace-only mapping counts as unmapped', async () => {
  const blank: ActionStatus = { ...st, action: { ...st.action, items: [{ id: 'i3', remoteName: 'blank', type: 'dir', excludes: null, localPaths: { m1: '   ' } }] } }
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [blank] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Items' }))
  const row = screen.getByText('blank').closest('tr')!
  expect(within(row).getByText('Not mapped on this machine')).toBeInTheDocument()
  expect(within(row).getByRole('button', { name: 'Map blank' })).toHaveTextContent('Map…')
  expect(within(row).queryByRole('button', { name: 'Unmap blank' })).not.toBeInTheDocument()
})

test('the auto-filled name follows the type and picked path until the user types a name', async () => {
  await openItems()
  const dlg = openAdd()
  await chooseFolder(dlg, '/Users/me/docs')
  expect(within(dlg).getByLabelText('Name in the bucket')).toHaveValue('docs')
  fireEvent.change(within(dlg).getByLabelText('Type'), { target: { value: 'file' } })
  bindings.PickFile.mockResolvedValue('/Users/me/a.txt')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Choose…' }))
  await waitFor(() => expect(within(dlg).getByLabelText('Name in the bucket')).toHaveValue('a.txt'))
  bindings.PickFile.mockResolvedValue('/Users/me/b.txt')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Choose…' }))
  await waitFor(() => expect(within(dlg).getByLabelText('Name in the bucket')).toHaveValue('b.txt'))
  fireEvent.change(within(dlg).getByLabelText('Name in the bucket'), { target: { value: 'mine' } })
  bindings.PickFile.mockResolvedValue('/Users/me/c.txt')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Choose…' }))
  await waitFor(() => expect(within(dlg).getByText('/Users/me/c.txt')).toBeInTheDocument())
  expect(within(dlg).getByLabelText('Name in the bucket')).toHaveValue('mine')
})

test('Remap uses the folder picker for a dir item and replaces the mapping; cancelling the picker does nothing', async () => {
  await openItems()
  bindings.PickFolder.mockResolvedValue('')
  fireEvent.click(screen.getByRole('button', { name: 'Remap snippets' }))
  await waitFor(() => expect(bindings.PickFolder).toHaveBeenCalledTimes(1))
  expect(bindings.MapItem).not.toHaveBeenCalled()
  bindings.PickFolder.mockResolvedValue('/Users/me/new-snippets')
  fireEvent.click(screen.getByRole('button', { name: 'Remap snippets' }))
  await waitFor(() => expect(bindings.MapItem).toHaveBeenCalledWith('a1', 'i1', '/Users/me/new-snippets'))
  expect(bindings.PickFile).not.toHaveBeenCalled()
})

test('a second Map click while the picker or the request is running does nothing; a failure is reported', async () => {
  await openItems()
  const pick = deferred<string>()
  bindings.PickFile.mockReturnValue(pick.promise)
  const btn = screen.getByRole('button', { name: 'Map settings.json' })
  fireEvent.click(btn)
  fireEvent.click(btn)
  await flush()
  expect(bindings.PickFile).toHaveBeenCalledTimes(1)
  const map = deferred<void>()
  bindings.MapItem.mockReturnValue(map.promise)
  await act(async () => { pick.resolve('/p/settings.json') })
  await flush()
  expect(btn).toBeDisabled()
  fireEvent.click(btn)
  await flush()
  expect(bindings.MapItem).toHaveBeenCalledTimes(1)
  await act(async () => { map.reject('{"code":"access-denied","message":"nope"}') })
  expect(await screen.findByText(/bucket refused access/)).toBeInTheDocument()
  expect(btn).toBeEnabled()
})

test('a protected-path refusal is shown inside the add dialog', async () => {
  await openItems()
  const dlg = openAdd()
  await chooseFolder(dlg, '/Users/me/Library/Application Support')
  expect(within(dlg).getByDisplayValue('Application Support')).toBeInTheDocument()
  bindings.AddItem.mockRejectedValue('{"code":"protected-path","message":"this folder contains s3-anysync\'s own data folder"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Add' }))
  expect(await within(dlg).findByText(/own data folder/)).toBeInTheDocument()
  expect(screen.getByRole('dialog', { name: 'Add item' })).toBeInTheDocument()
})

test('Add sends the normalized name, an excludes array, and ignores a stale dir excludes text for a file', async () => {
  await openItems()
  const dlg = openAdd()
  await chooseFolder(dlg, '/Users/me/docs')
  fireEvent.change(within(dlg).getByLabelText('Name in the bucket'), { target: { value: ' my\\docs/ ' } })
  expect(within(dlg).getByText(/stored as "my\/docs"/)).toBeInTheDocument()
  fireEvent.change(within(dlg).getByLabelText('Excludes'), { target: { value: '*.tmp\n\n node_modules ' } })
  bindings.AddItem.mockResolvedValue({})
  fireEvent.click(within(dlg).getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(bindings.AddItem).toHaveBeenCalledWith('a1', { item: { remoteName: 'my/docs', type: 'dir', excludes: ['*.tmp', 'node_modules'] }, localPath: '/Users/me/docs' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

  const dlg2 = openAdd()
  fireEvent.change(within(dlg2).getByLabelText('Excludes'), { target: { value: '*.tmp' } })
  fireEvent.change(within(dlg2).getByLabelText('Type'), { target: { value: 'file' } })
  bindings.PickFile.mockResolvedValue('/Users/me/a.txt')
  fireEvent.click(within(dlg2).getByRole('button', { name: 'Choose…' }))
  await waitFor(() => expect(within(dlg2).getByDisplayValue('a.txt')).toBeInTheDocument())
  fireEvent.click(within(dlg2).getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(bindings.AddItem).toHaveBeenLastCalledWith('a1', { item: { remoteName: 'a.txt', type: 'file', excludes: [] }, localPath: '/Users/me/a.txt' }))
})

test.each(['snippets', 'snippets/sub', 'settings.json/x', 'a/../snippets', '..', '.', '  /  '])('the name %j is refused in the dialog without calling the agent', async (name) => {
  await openItems()
  const dlg = openAdd()
  await chooseFolder(dlg, '/Users/me/x')
  fireEvent.change(within(dlg).getByLabelText('Name in the bucket'), { target: { value: name } })
  expect(within(dlg).getByRole('alert')).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Add' })).toBeDisabled()
  expect(bindings.AddItem).not.toHaveBeenCalled()
})

test('while Add runs the dialog cannot be closed and a second click is ignored; a failure re-enables it', async () => {
  await openItems()
  const dlg = openAdd()
  await chooseFolder(dlg, '/Users/me/docs')
  const req = deferred<unknown>()
  bindings.AddItem.mockReturnValue(req.promise)
  const add = within(dlg).getByRole('button', { name: 'Add' })
  fireEvent.click(add)
  fireEvent.click(add)
  await flush()
  expect(bindings.AddItem).toHaveBeenCalledTimes(1)
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog', { name: 'Add item' })).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  await act(async () => { req.reject('{"code":"invalid","message":"items overlap"}') })
  expect(await within(dlg).findByText(/items overlap/)).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Add' })).toBeEnabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a picker result that arrives after the type was switched is ignored', async () => {
  await openItems()
  const dlg = openAdd()
  const pick = deferred<string>()
  bindings.PickFolder.mockReturnValue(pick.promise)
  fireEvent.click(within(dlg).getByRole('button', { name: 'Choose…' }))
  fireEvent.change(within(dlg).getByLabelText('Type'), { target: { value: 'file' } })
  await act(async () => { pick.resolve('/Users/me/a-folder') })
  expect(within(dlg).getByText('nothing chosen')).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Add' })).toBeDisabled()
})

test('Unmap asks first, calls UnmapItem, and cannot be dismissed while running', async () => {
  await openItems()
  expect(screen.queryByRole('button', { name: 'Unmap settings.json' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Unmap snippets' }))
  const dlg = screen.getByRole('dialog', { name: 'Stop syncing snippets here?' })
  expect(within(dlg).getByText(/Local files and the cloud copy stay/)).toBeInTheDocument()
  const req = deferred<void>()
  bindings.UnmapItem.mockReturnValue(req.promise)
  const ok = within(dlg).getByRole('button', { name: 'Unmap' })
  fireEvent.click(ok)
  fireEvent.click(ok)
  await flush()
  expect(bindings.UnmapItem).toHaveBeenCalledTimes(1)
  expect(bindings.UnmapItem).toHaveBeenCalledWith('a1', 'i1')
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  await act(async () => { req.resolve() })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('Remove calls RemoveItem; a failure closes the dialog and shows the error', async () => {
  await openItems()
  fireEvent.click(screen.getByRole('button', { name: 'Remove settings.json' }))
  const dlg = screen.getByRole('dialog', { name: 'Remove settings.json?' })
  expect(within(dlg).getByText(/every machine/)).toBeInTheDocument()
  bindings.RemoveItem.mockRejectedValue('{"code":"locked","message":"held"}')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Remove' }))
  await waitFor(() => expect(bindings.RemoveItem).toHaveBeenCalledWith('a1', 'i2'))
  expect(await screen.findByText(/Another machine is syncing/)).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('shows a spinner, not "not mapped", until the machine id is known', async () => {
  const status = deferred<unknown>()
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockReturnValue(status.promise)
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  await waitFor(() => expect(bindings.Status).toHaveBeenCalled())
  await act(async () => { emit('agent:event', { type: 'action.state', actionId: 'a1', status: st, time: 't' }) })
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Items' }))
  expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument()
  expect(screen.queryByText('Not mapped on this machine')).not.toBeInTheDocument()
  await act(async () => { status.resolve({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [st] }) })
  expect(await screen.findByText('/Users/me/snippets')).toBeInTheDocument()
})
