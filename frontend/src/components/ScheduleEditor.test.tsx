import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { useState } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import { ScheduleEditor } from './ScheduleEditor'
import type { Schedule } from '../lib/types'

function Host({ initial }: { initial: Schedule }) {
  const [v, setV] = useState(initial)
  return <ScheduleEditor value={v} onChange={setV} />
}

test('kind none never calls the agent and shows no preview', async () => {
  render(<Host initial={{ kind: 'none' }} />)
  await new Promise((r) => setTimeout(r, 450))
  expect(bindings.PreviewSchedule).not.toHaveBeenCalled()
  expect(screen.queryByText(/Next runs/)).toBeNull()
})

test('an invalid cron shows the agent message', async () => {
  bindings.PreviewSchedule.mockRejectedValue('{"code":"invalid","message":"cron: expected 5 fields"}')
  render(<Host initial={{ kind: 'cron', cron: 'nope' }} />)
  expect(await screen.findByText('cron: expected 5 fields')).toBeInTheDocument()
  expect(bindings.PreviewSchedule).toHaveBeenCalledWith({ kind: 'cron', cron: 'nope' })
})

test('an interval below one minute is an error from the agent', async () => {
  bindings.PreviewSchedule.mockImplementation(async (s: Schedule) => {
    if ((s.everyMinutes ?? 0) < 1) throw '{"code":"invalid","message":"interval must be at least 1 minute"}'
    return ['2026-10-04T10:00:00Z']
  })
  render(<Host initial={{ kind: 'interval', everyMinutes: 45 }} />)
  expect(await screen.findByText(/Next runs: .*(2026|2025|10)/)).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Minutes between runs'), { target: { value: '0' } })
  expect(await screen.findByText('interval must be at least 1 minute')).toBeInTheDocument()
})

test('a stale preview response is ignored', async () => {
  let first!: (v: string[]) => void
  bindings.PreviewSchedule
    .mockImplementationOnce(() => new Promise<string[]>((r) => { first = r }))
    .mockResolvedValue(['2030-01-01T00:00:00Z'])
  render(<Host initial={{ kind: 'interval', everyMinutes: 45 }} />)
  await waitFor(() => expect(bindings.PreviewSchedule).toHaveBeenCalledTimes(1))
  fireEvent.change(screen.getByLabelText('Minutes between runs'), { target: { value: '50' } })
  await waitFor(() => expect(bindings.PreviewSchedule).toHaveBeenCalledTimes(2))
  expect(await screen.findByText(/2030|2029/)).toBeInTheDocument()
  first(['1999-01-01T00:00:00Z'])
  await new Promise((r) => setTimeout(r, 20))
  expect(screen.queryByText(/1999|1998/)).toBeNull()
})
