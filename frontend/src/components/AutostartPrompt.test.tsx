import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { bindings } from '../test/mockBridge'
import App from '../App'

const base = { machineId: 'm1', machineName: 'mac', autostart: false, autostartSupported: true, autostartOffered: false, logLevel: 'info' }
const dialog = { name: 'Start s3-anysync at login?' }

function boot(settings: object, strict = false) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  bindings.Settings.mockResolvedValue(settings)
  return render(strict ? <StrictMode><App /></StrictMode> : <App />)
}

test('asks once and enables on Yes', async () => {
  boot(base)
  bindings.UpdateSettings.mockResolvedValue({ ...base, autostart: true, autostartOffered: true })
  fireEvent.click(await screen.findByRole('button', { name: 'Yes, start at login' }))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ ...base, autostart: true, autostartOffered: true }))
  await waitFor(() => expect(screen.queryByRole('dialog', dialog)).toBeNull())
})

test('Not now records the answer without enabling', async () => {
  boot(base)
  bindings.UpdateSettings.mockResolvedValue({ ...base, autostartOffered: true })
  fireEvent.click(await screen.findByRole('button', { name: 'Not now' }))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ ...base, autostartOffered: true }))
  await waitFor(() => expect(screen.queryByRole('dialog', dialog)).toBeNull())
})

test.each([
  ['unsupported', { ...base, autostartSupported: false }],
  ['already answered', { ...base, autostartOffered: true }],
  ['already on', { ...base, autostart: true }],
])('not shown when %s', async (_, s) => {
  boot(s)
  await waitFor(() => expect(bindings.Settings).toHaveBeenCalled())
  expect(screen.queryByRole('dialog', dialog)).toBeNull()
})

test('a failing enable shows the error and closes the prompt', async () => {
  boot(base)
  bindings.UpdateSettings.mockRejectedValue('{"code":"internal","message":"launchctl failed"}')
  fireEvent.click(await screen.findByRole('button', { name: 'Yes, start at login' }))
  expect(await screen.findByText('Something went wrong.')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('dialog', dialog)).toBeNull())
})

test('Escape is ignored while the request is in flight, and works afterwards', async () => {
  boot(base)
  let finish: (v: unknown) => void = () => {}
  bindings.UpdateSettings.mockReturnValue(new Promise((r) => { finish = r }))
  fireEvent.click(await screen.findByRole('button', { name: 'Yes, start at login' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Not now' })).toBeDisabled())
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.getByRole('dialog', dialog)).toBeInTheDocument()
  expect(bindings.UpdateSettings).toHaveBeenCalledTimes(1)
  finish({ ...base, autostart: true, autostartOffered: true })
  await waitFor(() => expect(screen.queryByRole('dialog', dialog)).toBeNull())
})

test('Escape dismisses without sending anything', async () => {
  boot(base)
  await screen.findByRole('dialog', dialog)
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.queryByRole('dialog', dialog)).toBeNull()
  expect(bindings.UpdateSettings).not.toHaveBeenCalled()
})

test('a failing or late settings load never shows the prompt or throws', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  bindings.Settings.mockRejectedValue('{"code":"unreachable","message":"down"}')
  render(<App />)
  await waitFor(() => expect(bindings.Settings).toHaveBeenCalled())
  expect(screen.queryByRole('dialog', dialog)).toBeNull()
})

test('a result arriving after unmount does not show the prompt', async () => {
  let resolve: (v: unknown) => void = () => {}
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  bindings.Settings.mockReturnValue(new Promise((r) => { resolve = r }))
  const { unmount } = render(<App />)
  await waitFor(() => expect(bindings.Settings).toHaveBeenCalled())
  unmount()
  resolve(base)
  await Promise.resolve()
  expect(screen.queryByRole('dialog', dialog)).toBeNull()
})

test('settings are loaded once per session even under StrictMode', async () => {
  boot(base, true)
  await screen.findByRole('dialog', dialog)
  expect(bindings.Settings).toHaveBeenCalledTimes(1)
})
