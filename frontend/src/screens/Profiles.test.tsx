import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
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
  fireEvent.click(await screen.findByText('Edit'))
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
  const remove = await screen.findByText('Remove')
  expect(remove).toBeDisabled()
  expect(remove).toHaveAttribute('title', 'Used by: Editor')
  act(() => emit('agent:event', { type: 'actions.changed', time: 't' }))
})
