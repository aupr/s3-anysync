import { vi } from 'vitest'
vi.mock('./lib/bridge', () => import('./test/mockBridge'))
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { bindings, emit } from './test/mockBridge'
import App from './App'

test('shows the agent-down page with a retry button', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: false, reason: 'the agent did not start within 10s' })
  bindings.DataDir.mockResolvedValue('/tmp/data')
  render(<App />)
  expect(await screen.findByText('the agent did not start within 10s')).toBeInTheDocument()
  fireEvent.click(screen.getByText('Start the agent and retry'))
  await waitFor(() => expect(bindings.RetryConnect).toHaveBeenCalled())
})

test('shows the shell once connected, and a banner when the agent goes away', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'studio-mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  expect(await screen.findByText('studio-mac')).toBeInTheDocument()
  act(() => emit('agent:connection', { connected: false, reason: 'the connection to the agent was lost' }))
  expect(await screen.findByText(/not reachable/)).toBeInTheDocument()
  expect(screen.getByText('studio-mac')).toBeInTheDocument()
})

test('the theme is applied on the agent-down page too', async () => {
  localStorage.setItem('s3as.theme', 'dark')
  document.documentElement.classList.remove('dark')
  bindings.ConnectionState.mockResolvedValue({ connected: false, reason: 'down' })
  bindings.DataDir.mockResolvedValue('/tmp/data')
  render(<App />)
  expect(await screen.findByText('down')).toBeInTheDocument()
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  localStorage.removeItem('s3as.theme')
})
