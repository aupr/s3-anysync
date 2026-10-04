import { normalizeSchedule, parseWhole, reapplyEdits, rulesOf, splitLines } from './rules'
import type { Action, Rules } from './types'

const base: Rules = { name: 'A', direction: 'two-way', triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, excludes: [], snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50 }

test('rulesOf copies the editable fields and normalizes null lists', () => {
  const a = { ...base, id: 'x', excludes: null, encrypt: false, items: null, machines: null, machineDirections: null, updatedAt: 't' } as unknown as Action
  expect(rulesOf(a)).toEqual(base)
})

test('reapplyEdits keeps the other machine\'s changes and re-applies mine', () => {
  const mine = { ...base, name: 'Mine', excludes: ['*.log'] }
  const theirs = { ...base, snapshotRetention: 5, name: 'Theirs' }
  expect(reapplyEdits(base, mine, theirs)).toEqual({ ...theirs, name: 'Mine', excludes: ['*.log'] })
})

test('normalizeSchedule drops the fields Go omits when zero (Sunday has no weekday)', () => {
  expect(normalizeSchedule({ kind: 'weekly', weekday: 0, at: '09:00' })).toEqual({ kind: 'weekly', at: '09:00' })
  expect(normalizeSchedule({ kind: 'weekly', weekday: 3, at: '09:00', everyMinutes: 0, cron: '' })).toEqual({ kind: 'weekly', weekday: 3, at: '09:00' })
  expect(normalizeSchedule({ kind: 'interval', everyMinutes: 15 })).toEqual({ kind: 'interval', everyMinutes: 15 })
  expect(normalizeSchedule({ kind: 'none' })).toEqual({ kind: 'none' })
})

test('rulesOf normalizes the schedule', () => {
  const a = { ...base, triggers: { ...base.triggers, schedule: { kind: 'weekly', weekday: 0, at: '09:00' } } } as unknown as Action
  expect(rulesOf(a).triggers.schedule).toEqual({ kind: 'weekly', at: '09:00' })
})

test('parseWhole accepts only whole numbers of at least 1', () => {
  expect(parseWhole('7')).toBe(7)
  expect(parseWhole(' 12 ')).toBe(12)
  for (const bad of ['', '  ', '0', '-3', '1.5', '1e3', 'abc', '99999999999999999999']) expect(parseWhole(bad)).toBeNull()
})

test('splitLines trims and drops empty lines', () => {
  expect(splitLines(' a \n\n b\n')).toEqual(['a', 'b'])
  expect(splitLines('')).toEqual([])
})
