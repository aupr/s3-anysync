import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'
import type { ActionStatus } from '../lib/types'

const action = (over: Partial<ActionStatus> = {}): ActionStatus => ({
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50, items: [], machines: { m1: 'mac' }, updatedAt: '2026-10-04T01:02:03.000000004Z' },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0, ...over,
})
const prof = (id: string, name: string) => ({ id, name, provider: 'minio', endpoint: '', region: '', bucket: 'b', rootPrefix: '', accessKeyId: 'k', pathStyle: true, conditionalWrites: true })

function boot(actions: ActionStatus[], profiles = [prof('p1', 'Home')]) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions })
  bindings.Profiles.mockResolvedValue(profiles)
  bindings.PreviewSchedule.mockResolvedValue([])
  render(<App />)
}

// Walks the New action wizard to its last step with the given name.
async function wizardToItems(name = 'VS Code') {
  fireEvent.click(await screen.findByText('New action'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Next'))
  fireEvent.change(within(dlg).getByLabelText('Name'), { target: { value: name } })
  fireEvent.click(within(dlg).getByText('Next'))
  fireEvent.click(within(dlg).getByText('Next')) // not encrypted
  return dlg
}

test('rows show state, counts as links, and the right buttons', async () => {
  boot([action({ state: 'paused-conflict', conflicts: 2 }), action({ action: { ...action().action, id: 'a2', name: 'Shell' }, running: true })])
  const editor = (await screen.findByText('Editor')).closest('tr')!
  expect(within(editor).getByText('Conflicts')).toBeInTheDocument()
  expect(within(editor).getByText('2 conflicts')).toBeInTheDocument()
  const shell = screen.getByText('Shell').closest('tr')!
  bindings.CancelSync.mockResolvedValue(undefined)
  fireEvent.click(within(shell).getByText('Cancel'))
  await waitFor(() => expect(bindings.CancelSync).toHaveBeenCalledWith('a2'))
})

test('per-row buttons have unique accessible names', async () => {
  boot([action(), action({ action: { ...action().action, id: 'a2', name: 'Shell' }, state: 'paused' })])
  await screen.findByText('Editor')
  expect(screen.getByRole('button', { name: 'Sync now Editor' })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Pause Editor' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sync now Shell' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: 'Resume Shell' }))
  await waitFor(() => expect(bindings.Resume).toHaveBeenCalledWith('a2'))
  fireEvent.click(screen.getByRole('button', { name: 'Pause Editor' }))
  await waitFor(() => expect(bindings.Pause).toHaveBeenCalledWith('a1'))
})

test('sync now reports a cancelled run quietly', async () => {
  boot([action()])
  bindings.Sync.mockRejectedValue('{"code":"cancelled","message":"cancelled by the user"}')
  fireEvent.click(await screen.findByRole('button', { name: 'Sync now Editor' }))
  expect(await screen.findByText('Sync cancelled.')).toBeInTheDocument()
})

test('a failed sync shows an error toast and re-enables the row', async () => {
  boot([action()])
  bindings.Sync.mockRejectedValue('{"code":"locked","message":"held by m2"}')
  fireEvent.click(await screen.findByRole('button', { name: 'Sync now Editor' }))
  expect(await screen.findByText(/Another machine is syncing this action/)).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Sync now Editor' })).toBeEnabled())
})

test('a sync cannot be started twice while one is being requested', async () => {
  boot([action()])
  let done!: (v: unknown) => void
  bindings.Sync.mockImplementation(() => new Promise((r) => { done = r }))
  const btn = await screen.findByRole('button', { name: 'Sync now Editor' })
  fireEvent.click(btn)
  await waitFor(() => expect(btn).toBeDisabled())
  fireEvent.click(btn)
  expect(bindings.Sync).toHaveBeenCalledTimes(1)
  done({ outcome: 'ok', uploaded: 1, downloaded: 0, deletedRemote: 0, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null })
  expect(await screen.findByText(/Synced: uploaded 1/)).toBeInTheDocument()
})

test('new action wizard creates the action and its first item', async () => {
  boot([])
  const dlg = await wizardToItems()
  bindings.PickFolder.mockResolvedValue('/Users/me/Library/Application Support/Code/User')
  fireEvent.click(within(dlg).getByText('Add folder'))
  expect(await within(dlg).findByDisplayValue('User')).toBeInTheDocument()
  bindings.CreateAction.mockResolvedValue({ ...action().action, id: 'new', name: 'VS Code' })
  bindings.AddItem.mockResolvedValue({ id: 'i1', remoteName: 'User', type: 'dir', excludes: [], localPaths: {} })
  bindings.Action.mockResolvedValue(action({ action: { ...action().action, id: 'new', name: 'VS Code' } }))
  fireEvent.click(within(dlg).getByText('Create'))
  await waitFor(() => expect(bindings.AddItem).toHaveBeenCalledWith('new', { item: { remoteName: 'User', type: 'dir', excludes: [] }, localPath: '/Users/me/Library/Application Support/Code/User' }))
  expect(bindings.CreateAction.mock.calls[0][0]).toMatchObject({ profileId: 'p1', action: { name: 'VS Code', direction: 'two-way', propagateDeletes: true, triggers: { onChange: true, schedule: { kind: 'none' } } } })
  expect(Object.keys(bindings.CreateAction.mock.calls[0][0].action).sort()).toEqual(['direction', 'name', 'propagateDeletes', 'triggers'])
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('new action wizard: a failed create is shown inline and nothing closes', async () => {
  boot([])
  const dlg = await wizardToItems()
  bindings.CreateAction.mockRejectedValue('{"code":"invalid","message":"name already used"}')
  fireEvent.click(within(dlg).getByText('Create'))
  expect(await within(dlg).findByText('name already used')).toBeInTheDocument()
  expect(within(dlg).getByText('Create')).toBeEnabled()
})

test('new action wizard: cancel and Escape are blocked while creating, and create is not doubled', async () => {
  boot([])
  const dlg = await wizardToItems()
  let done!: (v: unknown) => void
  bindings.CreateAction.mockImplementation(() => new Promise((r) => { done = r }))
  fireEvent.click(within(dlg).getByText('Create'))
  await waitFor(() => expect(within(dlg).getByText('Create')).toBeDisabled())
  expect(within(dlg).getByText('Cancel')).toBeDisabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  fireEvent.click(within(dlg).getByText('Create'))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(bindings.CreateAction).toHaveBeenCalledTimes(1)
  done({ ...action().action, id: 'new', name: 'VS Code' })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('new action wizard: items that fail to add leave the action created with a visible message', async () => {
  boot([])
  const dlg = await wizardToItems()
  bindings.PickFolder.mockResolvedValue('/x/User')
  fireEvent.click(within(dlg).getByText('Add folder'))
  await within(dlg).findByDisplayValue('User')
  bindings.CreateAction.mockResolvedValue({ ...action().action, id: 'new', name: 'VS Code' })
  bindings.AddItem.mockRejectedValue('{"code":"protected-path","message":"path is protected"}')
  fireEvent.click(within(dlg).getByText('Create'))
  expect(await screen.findByText(/The action was created, but some items were not added: User: path is protected/)).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('new action wizard: validates the name and a failed folder pick is shown', async () => {
  boot([])
  fireEvent.click(await screen.findByText('New action'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Next'))
  fireEvent.click(within(dlg).getByText('Next'))
  expect(await within(dlg).findByText('Give the action a name.')).toBeInTheDocument()
  fireEvent.change(within(dlg).getByLabelText('Name'), { target: { value: 'X' } })
  fireEvent.click(within(dlg).getByText('Next'))
  fireEvent.click(within(dlg).getByText('Next'))
  bindings.PickFolder.mockRejectedValue('{"code":"unsupported","message":"no dialog"}')
  fireEvent.click(within(dlg).getByText('Add folder'))
  expect(await within(dlg).findByText(/not supported on this system/)).toBeInTheDocument()
})

test('new action wizard without profiles points to Profiles', async () => {
  boot([], [])
  fireEvent.click(await screen.findByText('New action'))
  const dlg = screen.getByRole('dialog')
  expect(within(dlg).getByText(/Create a profile/)).toBeInTheDocument()
  fireEvent.click(within(dlg).getByText('Go to Profiles'))
  expect(await screen.findByText('New profile')).toBeInTheDocument()
})

test('join lists only actions this machine has not joined', async () => {
  boot([action()])
  bindings.Discover.mockResolvedValue([action().action, { ...action().action, id: 'a9', name: 'Terminal', encrypt: true }])
  fireEvent.click(await screen.findByText('Join existing'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Find actions'))
  expect(await within(dlg).findByText('Terminal')).toBeInTheDocument()
  expect(within(dlg).queryByText('Editor')).toBeNull()
})

test('join: a failed discover is shown, never stuck', async () => {
  boot([])
  bindings.Discover.mockRejectedValue('{"code":"access-denied","message":"403"}')
  fireEvent.click(await screen.findByText('Join existing'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Find actions'))
  expect(await within(dlg).findByText(/The bucket refused access/)).toBeInTheDocument()
  expect(within(dlg).getByText('Find actions')).toBeEnabled()
})

test('join: a discover for a previous profile is ignored', async () => {
  boot([], [prof('p1', 'Home'), prof('p2', 'Work')])
  let first!: (v: unknown) => void
  bindings.Discover.mockImplementationOnce(() => new Promise((r) => { first = r }))
  fireEvent.click(await screen.findByText('Join existing'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Find actions'))
  await waitFor(() => expect(bindings.Discover).toHaveBeenCalledWith('p1'))
  fireEvent.change(within(dlg).getByLabelText('Profile'), { target: { value: 'p2' } })
  expect(within(dlg).getByText('Find actions')).toBeEnabled()
  first([{ ...action().action, id: 'a9', name: 'Terminal' }])
  await new Promise((r) => setTimeout(r, 20))
  expect(within(dlg).queryByText('Terminal')).toBeNull()
})

test('join: joins, maps an item and navigates; cancel/Escape blocked while joining', async () => {
  boot([])
  const remote = { ...action().action, id: 'a9', name: 'Terminal', encrypt: true, items: [{ id: 'i1', remoteName: 'zsh', type: 'file' as const, excludes: null, localPaths: { m2: '/Users/o/.zshrc' } }], machines: { m2: 'other' } }
  bindings.Discover.mockResolvedValue([remote])
  fireEvent.click(await screen.findByText('Join existing'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Find actions'))
  fireEvent.click(await within(dlg).findByRole('radio'))
  expect(within(dlg).getByText('Join')).toBeDisabled() // passphrase needed
  fireEvent.change(within(dlg).getByLabelText('Passphrase'), { target: { value: 'secret123' } })
  let done!: (v: unknown) => void
  bindings.JoinAction.mockImplementation(() => new Promise((r) => { done = r }))
  fireEvent.click(within(dlg).getByText('Join'))
  await waitFor(() => expect(within(dlg).getByText('Join')).toBeDisabled())
  expect(within(dlg).getByText('Cancel')).toBeDisabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(bindings.JoinAction).toHaveBeenCalledWith({ profileId: 'p1', actionId: 'a9', passphrase: 'secret123' })
  done(remote)
  expect(await screen.findByText('Map Terminal on this machine')).toBeInTheDocument()
  bindings.PickFile.mockResolvedValue('/Users/me/.zshrc')
  fireEvent.click(screen.getByText('Choose…'))
  expect(await screen.findByText('/Users/me/.zshrc')).toBeInTheDocument()
  bindings.MapItem.mockRejectedValue('{"code":"root-missing","message":"gone"}')
  fireEvent.click(screen.getByText('Finish'))
  await waitFor(() => expect(bindings.MapItem).toHaveBeenCalledWith('a9', 'i1', '/Users/me/.zshrc'))
  expect(await screen.findByText(/Some items were not mapped/)).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('join: a failed join is shown inline', async () => {
  boot([])
  bindings.Discover.mockResolvedValue([{ ...action().action, id: 'a9', name: 'Terminal' }])
  bindings.JoinAction.mockRejectedValue('{"code":"decrypt","message":"bad"}')
  fireEvent.click(await screen.findByText('Join existing'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Find actions'))
  fireEvent.click(await within(dlg).findByRole('radio'))
  fireEvent.click(within(dlg).getByText('Join'))
  expect(await within(dlg).findByText(/passphrase is wrong/)).toBeInTheDocument()
  expect(within(dlg).getByText('Join')).toBeEnabled()
})
