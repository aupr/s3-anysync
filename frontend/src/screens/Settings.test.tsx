import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'
import type { Settings } from '../lib/types'

const base: Settings = { machineId: 'm1', machineName: 'mac', autostart: false, autostartSupported: true, logLevel: 'info' }

function boot() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v1', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  bindings.DataDir.mockResolvedValue('/tmp/d')
  render(<App />)
}

async function openSettings() {
  fireEvent.click(await screen.findByRole('button', { name: 'Settings' }))
}

test('settings: autostart hidden when unsupported; save sends the whole value', async () => {
  boot()
  bindings.Settings.mockResolvedValue({ ...base, autostartSupported: false })
  await openSettings()
  expect(await screen.findByDisplayValue('mac')).toBeInTheDocument()
  expect(screen.queryByLabelText('Start the agent when I log in')).toBeNull()
  fireEvent.change(screen.getByLabelText('Machine name'), { target: { value: 'studio' } })
  fireEvent.change(screen.getByLabelText('Log level'), { target: { value: 'debug' } })
  bindings.UpdateSettings.mockResolvedValue({ ...base, autostartSupported: false, machineName: 'studio', logLevel: 'debug' })
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ machineId: 'm1', machineName: 'studio', autostart: false, autostartSupported: false, logLevel: 'debug' }))
  expect(await screen.findByText('Settings saved.')).toBeInTheDocument()
})

test('log level options are exactly the values the agent accepts', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  await openSettings()
  const sel = await screen.findByLabelText('Log level')
  expect(within(sel).getAllByRole('option').map((o) => (o as HTMLOptionElement).value)).toEqual(['debug', 'info', 'warn', 'error'])
})

test('autostart shows a hint and is sent when toggled', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  await openSettings()
  const box = await screen.findByLabelText('Start the agent when I log in')
  expect(screen.getByText('Registers this app to start at login for your user account.')).toBeInTheDocument()
  fireEvent.click(box)
  bindings.UpdateSettings.mockResolvedValue({ ...base, autostart: true })
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ ...base, autostart: true }))
})

test('a failing load shows the message with Retry, not a spinner', async () => {
  boot()
  bindings.Settings.mockRejectedValueOnce('{"code":"unreachable","message":"down"}')
  await openSettings()
  expect(await screen.findByText('The agent is not running.')).toBeInTheDocument()
  expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull()
  bindings.Settings.mockResolvedValueOnce(base)
  fireEvent.click(screen.getByText('Retry'))
  expect(await screen.findByDisplayValue('mac')).toBeInTheDocument()
  expect(screen.queryByText('The agent is not running.')).toBeNull()
})

test('a blank machine name is blocked client-side', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  await openSettings()
  fireEvent.change(await screen.findByLabelText('Machine name'), { target: { value: '   ' } })
  fireEvent.click(screen.getByText('Save'))
  expect(await screen.findByText('The machine name cannot be empty.')).toBeInTheDocument()
  expect(bindings.UpdateSettings).not.toHaveBeenCalled()
})

test('a failed save shows the error inline and keeps the form dirty and editable', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  await openSettings()
  fireEvent.change(await screen.findByLabelText('Machine name'), { target: { value: 'studio' } })
  bindings.UpdateSettings.mockRejectedValueOnce('{"code":"invalid","message":"invalid: machine name taken"}')
  fireEvent.click(screen.getByText('Save'))
  expect(await screen.findByText('invalid: machine name taken')).toBeInTheDocument()
  expect(screen.getByLabelText('Machine name')).toHaveValue('studio')
  expect(screen.getByText('Save')).toBeEnabled()
  bindings.UpdateSettings.mockResolvedValueOnce({ ...base, machineName: 'studio' })
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(screen.queryByText('invalid: machine name taken')).toBeNull())
  expect(bindings.UpdateSettings).toHaveBeenCalledTimes(2)
})

test('when enabling autostart fails the checkbox returns to its previous value', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  await openSettings()
  const box = await screen.findByLabelText('Start the agent when I log in')
  fireEvent.click(box)
  expect(box).toBeChecked()
  bindings.UpdateSettings.mockRejectedValueOnce('{"code":"internal","message":"launchctl failed"}')
  fireEvent.click(screen.getByText('Save'))
  expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong.')
  expect(screen.getByLabelText('Start the agent when I log in')).not.toBeChecked()
})

test('Save cannot be double submitted and shows a busy state', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  await openSettings()
  fireEvent.change(await screen.findByLabelText('Machine name'), { target: { value: 'studio' } })
  let done!: (s: Settings) => void
  bindings.UpdateSettings.mockReturnValueOnce(new Promise<Settings>((r) => { done = r }))
  fireEvent.click(screen.getByText('Save'))
  const btn = await screen.findByText('Saving…')
  expect(btn).toBeDisabled()
  fireEvent.click(btn)
  fireEvent.click(btn)
  expect(bindings.UpdateSettings).toHaveBeenCalledTimes(1)
  done({ ...base, machineName: 'studio' })
  expect(await screen.findByText('Save')).toBeEnabled()
})

test('a load that resolves after leaving the screen is dropped quietly', async () => {
  const err = vi.spyOn(console, 'error').mockImplementation(() => {})
  boot()
  let done!: (s: Settings) => void
  bindings.Settings.mockReturnValueOnce(new Promise<Settings>((r) => { done = r }))
  await openSettings()
  fireEvent.click(await screen.findByRole('button', { name: 'Activity' }))
  done(base)
  await new Promise((r) => setTimeout(r, 10))
  expect(screen.queryByDisplayValue('mac')).toBeNull()
  expect(err).not.toHaveBeenCalled()
  err.mockRestore()
})

test('the data folder can be opened and a failure is toasted', async () => {
  boot()
  bindings.Settings.mockResolvedValue(base)
  bindings.OpenDataFolder.mockRejectedValueOnce('{"code":"internal","message":"no finder"}')
  await openSettings()
  expect(await screen.findByText('/tmp/d')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Open data folder' }))
  expect(await screen.findByText('Something went wrong.')).toBeInTheDocument()
})
