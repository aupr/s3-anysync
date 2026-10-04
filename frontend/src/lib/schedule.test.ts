import { presetOf, scheduleFor } from './schedule'

test('presets round-trip', () => {
  expect(presetOf({ kind: 'none' })).toBe('none')
  expect(presetOf({ kind: 'interval', everyMinutes: 15 })).toBe('every15')
  expect(presetOf({ kind: 'interval', everyMinutes: 60 })).toBe('hourly')
  expect(presetOf({ kind: 'interval', everyMinutes: 45 })).toBe('interval')
  expect(presetOf({ kind: 'weekly', weekday: 1, at: '10:00' })).toBe('weekly')
  expect(scheduleFor('daily', { kind: 'none' })).toEqual({ kind: 'daily', at: '09:00' })
  expect(scheduleFor('daily', { kind: 'weekly', weekday: 3, at: '07:30' })).toEqual({ kind: 'daily', at: '07:30' })
  expect(scheduleFor('cron', { kind: 'none' })).toEqual({ kind: 'cron', cron: '0 * * * *' })
  expect(scheduleFor('every15', { kind: 'daily', at: '09:00' })).toEqual({ kind: 'interval', everyMinutes: 15 })
})

test('a weekly schedule without a weekday is Sunday', () => {
  expect(scheduleFor('weekly', { kind: 'weekly', at: '07:30' })).toEqual({ kind: 'weekly', weekday: 0, at: '07:30' })
  expect(scheduleFor('weekly', { kind: 'daily', at: '07:30' })).toEqual({ kind: 'weekly', weekday: 1, at: '07:30' })
})
