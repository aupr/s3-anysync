import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings, emit } from '../../test/mockBridge'
import App from '../../App'
import type { ActionStatus } from '../../lib/types'

const V1 = '2026-10-04T01:02:03.000000004Z'
const st = (over: Partial<ActionStatus['action']> = {}): ActionStatus => ({
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50, items: [], machines: { m1: 'mac' }, updatedAt: V1, ...over },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0,
})

async function openRules(first: ActionStatus = st()) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [first] })
  bindings.Profiles.mockResolvedValue([])
  bindings.PreviewSchedule.mockResolvedValue([])
  const r = render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Rules' }))
  return r
}
const save = () => screen.getByRole('button', { name: 'Save' })
const deferred = <T,>() => {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

test('save sends the loaded version verbatim', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Editor 2' } })
  bindings.UpdateRules.mockResolvedValue(st({ name: 'Editor 2', updatedAt: '2026-10-04T02:00:00Z' }).action)
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalled())
  const [id, req] = bindings.UpdateRules.mock.calls[0]
  expect(id).toBe('a1')
  expect(req.expectedUpdatedAt).toBe(V1)
  expect(req.rules.name).toBe('Editor 2')
  expect(req.rules.excludes).toEqual([])
  expect(bindings.SetMachineDirection).not.toHaveBeenCalled()
  expect(await screen.findByText('Rules saved.')).toBeInTheDocument()
})

test('conflict-edit: reload and re-apply keeps my edit on top of the fresh rules', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  bindings.UpdateRules.mockRejectedValueOnce('{"code":"conflict-edit","message":"changed"}')
  bindings.Action.mockResolvedValue(st({ snapshotRetention: 7, updatedAt: '2026-10-04T03:00:00Z' }))
  fireEvent.click(screen.getByText('Save'))
  const dlg = await screen.findByRole('dialog', { name: 'Changed elsewhere' })
  fireEvent.click(within(dlg).getByText('Reload and re-apply my edits'))
  await waitFor(() => expect(screen.getByLabelText('Manual snapshots to keep')).toHaveValue(7))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Name')).toHaveValue('Mine')
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'Mine', updatedAt: '2026-10-04T04:00:00Z' }).action)
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenLastCalledWith('a1', expect.objectContaining({ expectedUpdatedAt: '2026-10-04T03:00:00Z' })))
})

test('conflict-edit: reload discarding my edits shows the fresh rules', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  bindings.UpdateRules.mockRejectedValueOnce('{"code":"conflict-edit","message":"changed"}')
  bindings.Action.mockResolvedValue(st({ name: 'Theirs', updatedAt: '2026-10-04T03:00:00Z' }))
  fireEvent.click(screen.getByText('Save'))
  const dlg = await screen.findByRole('dialog', { name: 'Changed elsewhere' })
  fireEvent.click(within(dlg).getByText('Reload (discard my edits)'))
  await waitFor(() => expect(screen.getByLabelText('Name')).toHaveValue('Theirs'))
  expect(save()).toBeDisabled()
})

test('the conflict dialog stays open and busy while the reload is in flight, and shows a failed reload', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  bindings.UpdateRules.mockRejectedValueOnce('{"code":"conflict-edit","message":"changed"}')
  fireEvent.click(screen.getByText('Save'))
  const dlg = await screen.findByRole('dialog', { name: 'Changed elsewhere' })
  const d = deferred<ActionStatus>()
  bindings.Action.mockReturnValueOnce(d.promise)
  fireEvent.click(within(dlg).getByText('Reload and re-apply my edits'))
  expect(within(dlg).getByText('Reload and re-apply my edits')).toBeDisabled()
  expect(within(dlg).getByText('Reload (discard my edits)')).toBeDisabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog', { name: 'Changed elsewhere' })).toBeInTheDocument()
  await act(async () => d.reject('{"code":"internal","message":"boom"}'))
  expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent('Something went wrong')
  expect(within(screen.getByRole('dialog')).getByText('Reload and re-apply my edits')).toBeEnabled()
  fireEvent.keyDown(window, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(screen.getByLabelText('Name')).toHaveValue('Mine')
})

test('machine direction is saved separately, after the rules', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Both' } })
  const order: string[] = []
  bindings.UpdateRules.mockImplementation(async () => { order.push('rules'); return st({ name: 'Both', updatedAt: '2026-10-04T02:00:00Z' }).action })
  bindings.SetMachineDirection.mockImplementation(async () => { order.push('direction') })
  bindings.Action.mockResolvedValue(st({ name: 'Both', machineDirections: { m1: 'cloud-to-local' }, updatedAt: '2026-10-04T02:00:05Z' }))
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.SetMachineDirection).toHaveBeenCalledWith('a1', 'cloud-to-local'))
  expect(order).toEqual(['rules', 'direction'])
})

test('only the machine direction changed: the rules are not re-published', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  bindings.Action.mockResolvedValue(st({ machineDirections: { m1: 'cloud-to-local' }, updatedAt: '2026-10-04T02:00:05Z' }))
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.SetMachineDirection).toHaveBeenCalledWith('a1', 'cloud-to-local'))
  expect(bindings.UpdateRules).not.toHaveBeenCalled()
  await waitFor(() => expect(save()).toBeDisabled())
})

test('resetting this machine to the default sends an empty direction', async () => {
  await openRules(st({ machineDirections: { m1: 'cloud-to-local' } }))
  expect(screen.getByLabelText('This machine')).toHaveValue('cloud-to-local')
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: '' } })
  fireEvent.click(save())
  await waitFor(() => expect(bindings.SetMachineDirection).toHaveBeenCalledWith('a1', ''))
})

test('a second save after a direction change uses the version re-read after the direction call', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'One' } })
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'One', updatedAt: '2026-10-04T02:00:00Z' }).action)
  bindings.Action.mockResolvedValue(st({ name: 'One', machineDirections: { m1: 'cloud-to-local' }, updatedAt: '2026-10-04T02:00:09Z' }))
  fireEvent.click(save())
  await screen.findByText('Rules saved.')
  expect(bindings.Action).toHaveBeenCalledWith('a1')
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Two' } })
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'Two', updatedAt: '2026-10-04T03:00:00Z' }).action)
  fireEvent.click(save())
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalledTimes(2))
  expect(bindings.UpdateRules.mock.calls[1][1].expectedUpdatedAt).toBe('2026-10-04T02:00:09Z')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

test('a failed direction change after saved rules is reported and stays dirty; the retry sets only the direction', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'One' } })
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'One', updatedAt: '2026-10-04T02:00:00Z' }).action)
  bindings.SetMachineDirection.mockRejectedValueOnce('{"code":"locked","message":"held"}')
  fireEvent.click(save())
  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('The rules were saved')
  expect(alert).toHaveTextContent('direction was not changed')
  expect(screen.queryByText('Rules saved.')).not.toBeInTheDocument()
  expect(screen.getByLabelText('This machine')).toHaveValue('cloud-to-local')
  expect(save()).toBeEnabled()
  // Retry: the rules are saved already, only the direction is sent.
  bindings.Action.mockResolvedValue(st({ name: 'One', machineDirections: { m1: 'cloud-to-local' }, updatedAt: '2026-10-04T02:00:09Z' }))
  fireEvent.click(save())
  await screen.findByText('Rules saved.')
  expect(bindings.SetMachineDirection).toHaveBeenCalledTimes(2)
  expect(bindings.UpdateRules).toHaveBeenCalledTimes(1)
  await waitFor(() => expect(save()).toBeDisabled())
})

test('if the version cannot be re-read after the direction call, the next rules save re-reads it first', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'One' } })
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'One', updatedAt: '2026-10-04T02:00:00Z' }).action)
  bindings.Action.mockRejectedValueOnce('{"code":"internal","message":"net"}')
  fireEvent.click(save())
  expect(await screen.findByText(/could not be re-read/)).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Two' } })
  bindings.Action.mockResolvedValueOnce(st({ name: 'One', machineDirections: { m1: 'cloud-to-local' }, updatedAt: '2026-10-04T02:00:09Z' }))
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'Two', updatedAt: '2026-10-04T03:00:00Z' }).action)
  fireEvent.click(save())
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalledTimes(2))
  expect(bindings.UpdateRules.mock.calls[1][1].expectedUpdatedAt).toBe('2026-10-04T02:00:09Z')
})

test('a conflict-edit stops before the machine direction is changed', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  bindings.UpdateRules.mockRejectedValueOnce('{"code":"conflict-edit","message":"changed"}')
  fireEvent.click(save())
  await screen.findByRole('dialog', { name: 'Changed elsewhere' })
  expect(bindings.SetMachineDirection).not.toHaveBeenCalled()
})

test('a failed save shows the error and the form can be saved again', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  bindings.UpdateRules.mockRejectedValueOnce('{"code":"access-denied","message":"403"}')
  fireEvent.click(save())
  expect(await screen.findByRole('alert')).toHaveTextContent('The bucket refused access')
  expect(save()).toBeEnabled()
})

test('double clicking Save sends one request', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  const d = deferred<ActionStatus['action']>()
  bindings.UpdateRules.mockReturnValue(d.promise)
  fireEvent.click(save())
  fireEvent.click(save())
  expect(save()).toBeDisabled()
  await act(async () => d.resolve(st({ name: 'Mine', updatedAt: '2026-10-04T02:00:00Z' }).action))
  expect(bindings.UpdateRules).toHaveBeenCalledTimes(1)
})

test('a save that finishes after the screen is gone does not warn or toast', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
  const r = await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  const d = deferred<ActionStatus['action']>()
  bindings.UpdateRules.mockReturnValue(d.promise)
  fireEvent.click(save())
  r.unmount()
  await act(async () => d.resolve(st({ name: 'Mine', updatedAt: '2026-10-04T02:00:00Z' }).action))
  expect(errors).not.toHaveBeenCalled()
  expect(document.body.textContent).toBe('')
  expect(screen.queryByText('Rules saved.')).not.toBeInTheDocument()
  errors.mockRestore()
})

test('Discard reloads from the latest version in the store, not the stale base', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  const V2 = '2026-10-04T05:00:00.000000001Z'
  bindings.Actions.mockResolvedValue([st({ name: 'Newer', updatedAt: V2 })])
  act(() => emit('agent:event', { type: 'actions.changed', time: 't' }))
  await waitFor(() => expect(bindings.Actions).toHaveBeenCalled())
  await act(async () => {})
  expect(screen.getByLabelText('Name')).toHaveValue('Mine')
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
  expect(screen.getByLabelText('Name')).toHaveValue('Newer')
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Final' } })
  bindings.UpdateRules.mockResolvedValue(st({ name: 'Final', updatedAt: '2026-10-04T06:00:00Z' }).action)
  fireEvent.click(save())
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalledWith('a1', expect.objectContaining({ expectedUpdatedAt: V2 })))
})

test('numbers are validated inline and never sent as 0 or NaN', async () => {
  await openRules()
  const retention = screen.getByLabelText('Manual snapshots to keep')
  fireEvent.change(retention, { target: { value: '' } })
  expect(screen.getByText('Enter a whole number of at least 1.')).toBeInTheDocument()
  expect(save()).toBeDisabled()
  fireEvent.change(retention, { target: { value: '0' } })
  expect(save()).toBeDisabled()
  fireEvent.change(retention, { target: { value: '2.5' } })
  expect(save()).toBeDisabled()
  fireEvent.change(retention, { target: { value: '20' } })
  fireEvent.change(screen.getByLabelText('Delete guard percent'), { target: { value: '101' } })
  expect(screen.getByText('Enter a whole number from 1 to 100.')).toBeInTheDocument()
  expect(save()).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Delete guard percent'), { target: { value: '50' } })
  fireEvent.change(screen.getByLabelText('Debounce seconds'), { target: { value: '' } })
  expect(save()).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Debounce seconds'), { target: { value: '8' } })
  fireEvent.change(screen.getByLabelText('Cloud poll minutes'), { target: { value: '-1' } })
  expect(save()).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Cloud poll minutes'), { target: { value: '9' } })
  fireEvent.change(screen.getByLabelText('Delete guard files'), { target: { value: '3' } })
  bindings.UpdateRules.mockResolvedValue(st({ updatedAt: '2026-10-04T02:00:00Z' }).action)
  fireEvent.click(save())
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalled())
  const r = bindings.UpdateRules.mock.calls[0][1].rules
  expect(r.triggers.debounceSeconds).toBe(8)
  expect(r.triggers.remotePollMinutes).toBe(9)
  expect(r.deleteGuardMinFiles).toBe(3)
  expect(r.snapshotRetention).toBe(20)
})

test('an empty name is rejected inline', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: '  ' } })
  expect(screen.getByText('Enter a name.')).toBeInTheDocument()
  expect(save()).toBeDisabled()
})

test('a Sunday weekly schedule (no weekday in the JSON) is not an edit', async () => {
  await openRules(st({ triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'weekly', at: '09:00' }, remotePollMinutes: 5 } }))
  expect(screen.getByLabelText('Day')).toHaveValue('0')
  expect(save()).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Day'), { target: { value: '0' } })
  expect(save()).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Day'), { target: { value: '3' } })
  expect(save()).toBeEnabled()
  fireEvent.change(screen.getByLabelText('Day'), { target: { value: '0' } })
  expect(save()).toBeDisabled()
})

test('an invalid schedule is reported inline and nothing is saved', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Schedule'), { target: { value: 'cron' } })
  bindings.PreviewSchedule.mockRejectedValue('{"code":"invalid","message":"schedule: bad cron"}')
  fireEvent.change(screen.getByLabelText('Cron expression'), { target: { value: 'nope' } })
  fireEvent.click(save())
  await waitFor(() => expect(screen.getAllByRole('alert').map((e) => e.textContent).join()).toContain('schedule: bad cron'))
  expect(bindings.UpdateRules).not.toHaveBeenCalled()
  expect(save()).toBeEnabled()
})

test('excludes are sent as an array of trimmed lines', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Excludes'), { target: { value: ' *.log \n\ncache/**\n' } })
  bindings.UpdateRules.mockResolvedValue(st({ excludes: ['*.log', 'cache/**'], updatedAt: '2026-10-04T02:00:00Z' }).action)
  fireEvent.click(save())
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalled())
  expect(bindings.UpdateRules.mock.calls[0][1].rules.excludes).toEqual(['*.log', 'cache/**'])
})

test('discard restores the saved values', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  fireEvent.change(screen.getByLabelText('Manual snapshots to keep'), { target: { value: '' } })
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
  expect(screen.getByLabelText('Name')).toHaveValue('Editor')
  expect(screen.getByLabelText('Manual snapshots to keep')).toHaveValue(20)
})
