import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { formatTime } from '../lib/format'
import { presetLabels, presetOf, scheduleFor, weekdays, type Preset } from '../lib/schedule'
import type { Schedule } from '../lib/types'
import { Field, Input, Select } from './ui'

export function ScheduleEditor({ value, onChange }: { value: Schedule; onChange: (s: Schedule) => void }) {
  const preset = presetOf(value)
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
        <Select aria-label="Schedule" value={preset} onChange={(e) => onChange(scheduleFor(e.target.value as Preset, value))}>
          {(Object.keys(presetLabels) as Preset[]).map((p) => <option key={p} value={p}>{presetLabels[p]}</option>)}
        </Select>
      </Field>
      {preset === 'interval' && (
        <Field label="Minutes between runs">
          <Input aria-label="Minutes between runs" type="number" min={1} value={value.everyMinutes ?? 30}
            onChange={(e) => onChange({ ...value, everyMinutes: Number(e.target.value) })} />
        </Field>
      )}
      {preset === 'weekly' && (
        <Field label="Day">
          <Select aria-label="Day" value={value.weekday ?? 1} onChange={(e) => onChange({ ...value, weekday: Number(e.target.value) })}>
            {weekdays.map((d, i) => <option key={d} value={i}>{d}</option>)}
          </Select>
        </Field>
      )}
      {(preset === 'daily' || preset === 'weekly') && (
        <Field label="Time (24h, HH:MM)">
          <Input aria-label="Time" value={value.at ?? ''} onChange={(e) => onChange({ ...value, at: e.target.value })} />
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
