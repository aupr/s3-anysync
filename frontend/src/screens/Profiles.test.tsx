import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'
import type { Profile } from '../lib/types'

const prof: Profile = { id: 'p1', name: 'Home MinIO', provider: 'minio', endpoint: 'http://nas:9000', region: 'us-east-1', bucket: 'cfg', rootPrefix: 'sync', accessKeyId: 'AK', pathStyle: true, conditionalWrites: true }

async function openProfiles(profiles: Profile[], actions: unknown[] = []) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions })
  bindings.Profiles.mockResolvedValue(profiles)
  render(<App />)
  fireEvent.click(await screen.findByRole('button', { name: 'Profiles' }))
}

test('new profile: preset fills the endpoint, secret is required, test result is shown', async () => {
  await openProfiles([])
  fireEvent.click(screen.getByText('New profile'))
  const dlg = screen.getByRole('dialog')
  fireEvent.change(within(dlg).getByLabelText('Provider'), { target: { value: 'minio' } })
  expect(within(dlg).getByLabelText('Endpoint')).toHaveValue('http://localhost:9000')
  expect(within(dlg).getByLabelText('Path-style addressing')).toBeChecked()
  fireEvent.change(within(dlg).getByLabelText('Name'), { target: { value: 'Home' } })
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'cfg' } })
  fireEvent.change(within(dlg).getByLabelText('Access key ID'), { target: { value: 'AK' } })
  fireEvent.click(within(dlg).getByText('Save'))
  expect(await within(dlg).findByText('The secret access key is required.')).toBeInTheDocument()
  expect(bindings.AddProfile).not.toHaveBeenCalled()

  bindings.AddProfile.mockResolvedValue({ ...prof, id: 'new' })
  bindings.TestProfile.mockResolvedValue({ list: true, put: true, get: true, delete: true, conditionalWrites: false })
  fireEvent.change(within(dlg).getByLabelText('Secret access key'), { target: { value: 'SK' } })
  fireEvent.click(within(dlg).getByText('Save'))
  await waitFor(() => expect(bindings.AddProfile).toHaveBeenCalled())
  expect(bindings.AddProfile.mock.calls[0][0]).toMatchObject({ profile: { name: 'Home', bucket: 'cfg', endpoint: 'http://localhost:9000', pathStyle: true }, secret: 'SK' })
  expect(await screen.findByText(/Conditional writes: no/)).toBeInTheDocument()
})

test('edit with a blank secret keeps the stored one; agent field errors show inline', async () => {
  await openProfiles([prof])
  fireEvent.click(await screen.findByRole('button', { name: 'Edit Home MinIO' }))
  const dlg = screen.getByRole('dialog')
  expect(within(dlg).getByLabelText('Secret access key')).toHaveValue('')
  bindings.UpdateProfile.mockRejectedValueOnce('{"code":"in-use","message":"the bucket and folder cannot change while actions use this profile: Editor"}')
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'other' } })
  fireEvent.click(within(dlg).getByText('Save'))
  expect(await within(dlg).findByText(/cannot change while actions use this profile: Editor/)).toBeInTheDocument()
  bindings.UpdateProfile.mockResolvedValueOnce(prof)
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'cfg' } })
  fireEvent.click(within(dlg).getByText('Save'))
  await waitFor(() => expect(bindings.UpdateProfile).toHaveBeenLastCalledWith({ profile: expect.objectContaining({ id: 'p1' }), secret: '' }))
})

test('remove is disabled while an action uses the profile', async () => {
  const used = { action: { id: 'a1', name: 'Editor' }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 }
  await openProfiles([prof], [used])
  const remove = await screen.findByRole('button', { name: 'Remove Home MinIO (used by: Editor)' })
  expect(remove).toBeDisabled()
  expect(remove).toHaveAttribute('title', 'Used by: Editor')
})

const probeOk = { list: true, put: true, get: true, delete: true, conditionalWrites: true }
const prof2: Profile = { ...prof, id: 'p2', name: 'Work R2' }

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function fillRequired(dlg: HTMLElement) {
  fireEvent.change(within(dlg).getByLabelText('Name'), { target: { value: 'Home' } })
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'cfg' } })
  fireEvent.change(within(dlg).getByLabelText('Access key ID'), { target: { value: 'AK' } })
  fireEvent.change(within(dlg).getByLabelText('Secret access key'), { target: { value: 'SK' } })
}

test('a stale probe result does not reopen a closed Test dialog', async () => {
  await openProfiles([prof])
  const d = deferred<typeof probeOk>()
  bindings.TestProfile.mockReturnValueOnce(d.promise)
  fireEvent.click(await screen.findByRole('button', { name: 'Test connection Home MinIO' }))
  const dlg = await screen.findByRole('dialog')
  fireEvent.click(within(dlg).getByRole('button', { name: 'Close' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  await act(async () => { d.resolve(probeOk) })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a stale probe result does not overwrite another profile\'s dialog', async () => {
  await openProfiles([prof, prof2])
  const a = deferred<typeof probeOk>()
  bindings.TestProfile.mockReturnValueOnce(a.promise)
  fireEvent.click(await screen.findByRole('button', { name: 'Test connection Home MinIO' }))
  bindings.TestProfile.mockResolvedValueOnce({ ...probeOk, conditionalWrites: false })
  fireEvent.click(screen.getByRole('button', { name: 'Test connection Work R2' }))
  expect(await screen.findByText(/Conditional writes: no/)).toBeInTheDocument()
  await act(async () => { a.resolve(probeOk) })
  expect(screen.getByRole('dialog', { name: 'Test Work R2' })).toBeInTheDocument()
  expect(screen.getByText(/Conditional writes: no/)).toBeInTheDocument()
  expect(screen.queryByText(/Conditional writes: yes/)).not.toBeInTheDocument()
})

test('Test connection dialog shows the checklist', async () => {
  await openProfiles([prof])
  bindings.TestProfile.mockResolvedValueOnce(probeOk)
  fireEvent.click(await screen.findByRole('button', { name: 'Test connection Home MinIO' }))
  expect(await screen.findByText(/Upload/)).toBeInTheDocument()
  expect(screen.getByText(/Conditional writes: yes/)).toBeInTheDocument()
})

test('Test connection failure closes the dialog and shows a toast', async () => {
  await openProfiles([prof])
  bindings.TestProfile.mockRejectedValueOnce('{"code":"access-denied","message":"x"}')
  fireEvent.click(await screen.findByRole('button', { name: 'Test connection Home MinIO' }))
  expect(await screen.findByText(/The bucket refused access/)).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('placeholder endpoints are rejected on save', async () => {
  await openProfiles([])
  fireEvent.click(screen.getByText('New profile'))
  const dlg = screen.getByRole('dialog')
  fireEvent.change(within(dlg).getByLabelText('Provider'), { target: { value: 'r2' } })
  fillRequired(dlg)
  fireEvent.click(within(dlg).getByText('Save'))
  expect(await within(dlg).findByText('Replace the <…> parts of the endpoint and region.')).toBeInTheDocument()
  expect(bindings.AddProfile).not.toHaveBeenCalled()
})

test('a failed post-save test shows saved + failure, and Done reloads', async () => {
  await openProfiles([])
  fireEvent.click(screen.getByText('New profile'))
  const dlg = screen.getByRole('dialog')
  fillRequired(dlg)
  bindings.AddProfile.mockResolvedValueOnce({ ...prof, id: 'new' })
  bindings.TestProfile.mockRejectedValueOnce('{"code":"access-denied","message":"x"}')
  fireEvent.click(within(dlg).getByText('Save'))
  const saved = await screen.findByRole('dialog', { name: 'Profile saved' })
  expect(await within(saved).findByText('The profile was saved, but the connection test failed.')).toBeInTheDocument()
  expect(within(saved).getByText(/The bucket refused access/)).toBeInTheDocument()
  expect(within(saved).queryByText(/Testing the connection/)).not.toBeInTheDocument()
  const before = bindings.Profiles.mock.calls.length
  fireEvent.click(within(saved).getByRole('button', { name: 'Done' }))
  await waitFor(() => expect(bindings.Profiles.mock.calls.length).toBeGreaterThan(before))
})

test('Escape and Cancel are ignored while saving', async () => {
  await openProfiles([prof])
  fireEvent.click(await screen.findByRole('button', { name: 'Edit Home MinIO' }))
  const dlg = screen.getByRole('dialog')
  const d = deferred<Profile>()
  bindings.UpdateProfile.mockReturnValueOnce(d.promise)
  fireEvent.click(within(dlg).getByText('Save'))
  await waitFor(() => expect(bindings.UpdateProfile).toHaveBeenCalled())
  expect(within(dlg).getByRole('button', { name: 'Cancel' })).toBeDisabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  await act(async () => { d.resolve(prof) })
})

test('remove confirm: success removes and reloads', async () => {
  await openProfiles([prof])
  fireEvent.click(await screen.findByRole('button', { name: 'Remove Home MinIO' }))
  const before = bindings.Profiles.mock.calls.length
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove' }))
  await waitFor(() => expect(bindings.RemoveProfile).toHaveBeenCalledWith('p1'))
  await waitFor(() => expect(bindings.Profiles.mock.calls.length).toBeGreaterThan(before))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('remove confirm: a double click sends one request', async () => {
  await openProfiles([prof])
  fireEvent.click(await screen.findByRole('button', { name: 'Remove Home MinIO' }))
  const d = deferred<void>()
  bindings.RemoveProfile.mockReturnValueOnce(d.promise)
  const confirm = within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove' })
  fireEvent.click(confirm)
  fireEvent.click(confirm)
  await waitFor(() => expect(bindings.RemoveProfile).toHaveBeenCalled())
  expect(bindings.RemoveProfile).toHaveBeenCalledTimes(1)
  await act(async () => { d.resolve() })
})

test('remove confirm: failure shows the agent message in a toast', async () => {
  await openProfiles([prof])
  bindings.RemoveProfile.mockRejectedValueOnce('{"code":"in-use","message":"used by Editor"}')
  fireEvent.click(await screen.findByRole('button', { name: 'Remove Home MinIO' }))
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove' }))
  expect(await screen.findByText('used by Editor')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('switching provider on an edited profile keeps a real endpoint', async () => {
  await openProfiles([prof])
  fireEvent.click(await screen.findByRole('button', { name: 'Edit Home MinIO' }))
  const dlg = screen.getByRole('dialog')
  fireEvent.change(within(dlg).getByLabelText('Provider'), { target: { value: 'custom' } })
  expect(within(dlg).getByLabelText('Endpoint')).toHaveValue('http://nas:9000')
})
