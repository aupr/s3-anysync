import type { Schedule } from './types'

export type Preset = 'none' | 'every15' | 'hourly' | 'interval' | 'daily' | 'weekly' | 'cron'

export const presetLabels: Record<Preset, string> = {
  none: 'No schedule',
  every15: 'Every 15 minutes',
  hourly: 'Every hour',
  interval: 'Every N minutes',
  daily: 'Daily at…',
  weekly: 'Weekly on…',
  cron: 'Cron expression',
}

export const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

export function presetOf(s: Schedule): Preset {
  switch (s.kind) {
    case 'interval':
      return s.everyMinutes === 15 ? 'every15' : s.everyMinutes === 60 ? 'hourly' : 'interval'
    case 'daily':
    case 'weekly':
    case 'cron':
      return s.kind
    default:
      return 'none'
  }
}

export function scheduleFor(p: Preset, cur: Schedule): Schedule {
  switch (p) {
    case 'none':
      return { kind: 'none' }
    case 'every15':
      return { kind: 'interval', everyMinutes: 15 }
    case 'hourly':
      return { kind: 'interval', everyMinutes: 60 }
    case 'interval':
      return { kind: 'interval', everyMinutes: cur.kind === 'interval' && cur.everyMinutes ? cur.everyMinutes : 30 }
    case 'daily':
      return { kind: 'daily', at: cur.at ?? '09:00' }
    case 'weekly':
      return { kind: 'weekly', weekday: cur.weekday ?? 1, at: cur.at ?? '09:00' }
    case 'cron':
      return { kind: 'cron', cron: cur.cron ?? '0 * * * *' }
  }
}
