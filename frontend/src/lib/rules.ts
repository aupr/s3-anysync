import type { Action, Rules, Schedule } from './types'

// normalizeSchedule mirrors the Go JSON (omitempty): zero and empty optional
// fields are absent, so a Sunday weekly schedule has no weekday. Comparing
// normalized values keeps the form from looking edited when it is not.
export function normalizeSchedule(s: Schedule): Schedule {
  const out: Schedule = { kind: s.kind }
  if (s.everyMinutes) out.everyMinutes = s.everyMinutes
  if (s.at) out.at = s.at
  if (s.weekday) out.weekday = s.weekday
  if (s.cron) out.cron = s.cron
  return out
}

export function rulesOf(a: Action): Rules {
  return {
    name: a.name,
    direction: a.direction,
    triggers: { ...a.triggers, schedule: normalizeSchedule(a.triggers.schedule) },
    propagateDeletes: a.propagateDeletes,
    receiverRestoresCloud: a.receiverRestoresCloud,
    excludes: [...(a.excludes ?? [])],
    snapshotRetention: a.snapshotRetention,
    deleteGuardMinFiles: a.deleteGuardMinFiles,
    deleteGuardPercent: a.deleteGuardPercent,
  }
}

// reapplyEdits copies every field the user changed (original → edited) onto
// the fresh rules another machine saved meanwhile.
export function reapplyEdits(original: Rules, edited: Rules, fresh: Rules): Rules {
  const out: Rules = { ...fresh }
  for (const k of Object.keys(edited) as (keyof Rules)[]) {
    if (JSON.stringify(edited[k]) !== JSON.stringify(original[k])) {
      ;(out as unknown as Record<string, unknown>)[k] = edited[k]
    }
  }
  return out
}

// parseWhole reads a whole number >= 1 from an input's text, else null (an
// empty or malformed input must never become 0 silently).
export function parseWhole(s: string): number | null {
  const t = s.trim()
  if (!/^\d+$/.test(t)) return null
  const n = Number(t)
  return Number.isSafeInteger(n) && n >= 1 ? n : null
}

export function splitLines(s: string): string[] {
  return s.split('\n').map((x) => x.trim()).filter(Boolean)
}
