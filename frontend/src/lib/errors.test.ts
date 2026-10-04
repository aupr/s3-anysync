import { describeError } from './errors'
import { AgentError } from './api'

test('known codes get a friendly message and keep the raw one as details', () => {
  const d = describeError(new AgentError('access-denied', 'AccessDenied: 403'))
  expect(d.message).toMatch(/refused access/)
  expect(d.details).toBe('AccessDenied: 403')
  expect(d.field).toBe(false)
})

test('field codes show the agent message inline', () => {
  expect(describeError(new AgentError('invalid', 'name is empty'))).toMatchObject({ field: true, message: 'name is empty' })
  expect(describeError(new AgentError('protected-path', 'contains the data folder'))).toMatchObject({ field: true })
})

test('in-use shows the agent message; unknown codes are generic with details', () => {
  expect(describeError(new AgentError('in-use', 'used by Editor')).message).toBe('used by Editor')
  expect(describeError('{"code":"weird","message":"x"}')).toMatchObject({ message: 'Something went wrong.', details: 'x' })
})
