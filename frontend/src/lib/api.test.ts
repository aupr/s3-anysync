import { vi } from 'vitest'
vi.mock('./bridge', () => import('../test/mockBridge'))
import { bindings } from '../test/mockBridge'
import { api, AgentError, toAgentError } from './api'

test('toAgentError parses the Go JSON error', () => {
  const e = toAgentError('{"code":"in-use","message":"used by Editor"}')
  expect(e).toBeInstanceOf(AgentError)
  expect(e.code).toBe('in-use')
  expect(e.message).toBe('used by Editor')
  expect(toAgentError(new Error('{"code":"busy","message":"b"}')).code).toBe('busy')
  expect(toAgentError('plain failure')).toMatchObject({ code: 'internal', message: 'plain failure' })
})

test('calls reject with AgentError and pass arguments through', async () => {
  bindings.UpdateRules.mockRejectedValue('{"code":"conflict-edit","message":"changed"}')
  const rules = { name: 'x' } as never
  await expect(api.updateRules('a1', rules, '2026-10-04T01:02:03.000000004Z')).rejects.toMatchObject({ code: 'conflict-edit' })
  expect(bindings.UpdateRules).toHaveBeenCalledWith('a1', { rules, expectedUpdatedAt: '2026-10-04T01:02:03.000000004Z' })
})

test('null lists become empty arrays', async () => {
  bindings.Profiles.mockResolvedValue(null)
  expect(await api.profiles()).toEqual([])
})

test('a binding that throws synchronously rejects with an AgentError', async () => {
  bindings.Pause.mockImplementation(() => { throw new Error('boom') })
  const p = api.pause('a')
  await expect(p).rejects.toMatchObject({ name: 'AgentError', code: 'internal', message: 'boom' })
})
