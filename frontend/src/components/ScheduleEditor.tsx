import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { formatTime } from '../lib/format'
import { presetLabels, presetOf, scheduleFor, weekdays, type Preset } from '../lib/schedule'
import type { Schedule } from '../lib/types'
import { Field, Input, Select } from './ui'

// wholeMinutes parses a whole number; anything else (empty, "1.5", "x") is
// left for the user to finish. 0 is passed on so the agent explains it.
function wholeMinutes(s: string): number | undefined {
  return /^\d+$/.test(s.trim()) ? Number(s.trim()) : undefined
}

function fits(p: Preset, v: Schedule): boolean {
  switch (p) {
    case 'none': return v.kind === 'none'
    case 'every15': return v.kind === 'interval' && v.everyMinutes === 15
    case 'hourly': return v.kind === 'interval' && v.everyMinutes === 60
    case 'interval': return v.kind === 'interval'
    default: return v.kind === p
  }
}

export function ScheduleEditor({ value, onChange }: { value: Schedule; onChange: (s: Schedule) => void }) {
  const [preset, setPreset] = useState<Preset>(() => presetOf(value))
  const [minutes, setMinutes] = useState(() => String(value.everyMinutes ?? 30))
  // The chosen preset is kept locally (typing 15 must not swap the input for
  // a preset); it follows the value only when the value no longer fits it.
  useEffect(() => {
    if (!fits(preset, value)) setPreset(presetOf(value))
    if (value.kind === 'interval' && value.everyMinutes !== undefined && value.everyMinutes !== wholeMinutes(minutes)) {
      setMinutes(String(value.everyMinutes))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  const [runs, setRuns] = useState<string[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    setRuns([])
    setError('')
    if (value.kind === 'none') return
    let live = true
    const t = setTimeout(() => {
      api.previewSchedule(value)
        .then((r) => live && (setRuns(r), setError('')))
        .catch((e) => live && (setRuns([]), setError(describeError(e).message)))
    }, 300)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [value])
  return (
    <div className="space-y-2">
      <Field label="Schedule">
        <Select aria-label="Schedule" value={preset} onChange={(e) => {
          const p = e.target.value as Preset
          const next = scheduleFor(p, value)
          setPreset(p)
          if (next.kind === 'interval' && next.everyMinutes !== undefined) setMinutes(String(next.everyMinutes))
          onChange(next)
        }}>
          {(Object.keys(presetLabels) as Preset[]).map((p) => <option key={p} value={p}>{presetLabels[p]}</option>)}
        </Select>
      </Field>
      {preset === 'interval' && (
        <Field label="Minutes between runs">
          <Input aria-label="Minutes between runs" inputMode="numeric" value={minutes}
            onChange={(e) => {
              setMinutes(e.target.value)
              const n = wholeMinutes(e.target.value)
              if (n !== undefined) onChange({ ...value, kind: 'interval', everyMinutes: n })
            }} />
        </Field>
      )}
      {preset === 'weekly' && (
        <Field label="Day">
          <Select aria-label="Day" value={value.weekday ?? 0} onChange={(e) => onChange({ ...value, weekday: Number(e.target.value) })}>
            {weekdays.map((d, i) => <option key={d} value={i}>{d}</option>)}
          </Select>
        </Field>
      )}
      {(preset === 'daily' || preset === 'weekly') && (
        <Field label="Time (24h, HH:MM)">
          <Input aria-label="Time (24h, HH:MM)" value={value.at ?? ''} onChange={(e) => onChange({ ...value, at: e.target.value })} />
        </Field>
      )}
      {preset === 'cron' && (
        <Field label="Cron expression" hint="Five fields: minute hour day-of-month month day-of-week.">
          <Input aria-label="Cron expression" value={value.cron ?? ''} onChange={(e) => onChange({ ...value, cron: e.target.value })} />
        </Field>
      )}
      {preset !== 'none' && (error
        ? <p role="alert" className="text-xs text-red-600">{error}</p>
        : <p className="text-xs text-zinc-500">Next runs: {runs.map(formatTime).join(' · ') || '…'}</p>)}
    </div>
  )
}
