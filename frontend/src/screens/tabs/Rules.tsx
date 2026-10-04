import { useEffect, useRef, useState } from 'react'
import { ScheduleEditor } from '../../components/ScheduleEditor'
import { useToast } from '../../components/toast'
import { Button, Checkbox, Field, Input, Modal, Select } from '../../components/ui'
import { api, toAgentError } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { directionLabels } from '../../lib/format'
import { useDirtyGuard } from '../../lib/router'
import { normalizeSchedule, parseWhole, reapplyEdits, rulesOf, splitLines } from '../../lib/rules'
import { useStore } from '../../lib/store'
import type { ActionStatus, Direction, Rules } from '../../lib/types'

// The numeric inputs keep their text, so an empty or half-typed value is shown
// as typed (and flagged) instead of silently becoming 0.
type NumKey = 'debounce' | 'poll' | 'retention' | 'files' | 'percent'
type Text = Record<NumKey, string>
const toText = (r: Rules): Text => ({
  debounce: String(r.triggers.debounceSeconds),
  poll: String(r.triggers.remotePollMinutes),
  retention: String(r.snapshotRetention),
  files: String(r.deleteGuardMinFiles),
  percent: String(r.deleteGuardPercent),
})
const whole = 'Enter a whole number of at least 1.'
const percentMsg = 'Enter a whole number from 1 to 100.'

interface Base { rules: Rules; version: string; machineDir: Direction | '' }
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)
// isNewer: later events about an older version must not replace a newer one.
const isNewer = (a: string, b: string) => {
  const x = Date.parse(a)
  const y = Date.parse(b)
  return Number.isNaN(x) || Number.isNaN(y) ? a !== b : x > y
}

export function RulesTab({ st }: { st: ActionStatus }) {
  const { state } = useStore()
  const toast = useToast()
  const machineId = state.status?.machineId ?? ''
  const id = st.action.id
  const loadedMachineDir = st.action.machineDirections?.[machineId] ?? ''
  const [base, setBase] = useState<Base>(() => ({ rules: rulesOf(st.action), version: st.action.updatedAt, machineDir: loadedMachineDir }))
  const [form, setForm] = useState<Rules>(base.rules)
  const [text, setText] = useState<Text>(() => toText(base.rules))
  const [machineDir, setMachineDir] = useState<Direction | ''>(loadedMachineDir)
  const [excludes, setExcludes] = useState(base.rules.excludes.join('\n'))
  const [error, setError] = useState('')
  const [conflictError, setConflictError] = useState('')
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  // True while the local version may be older than the bucket's (the machine
  // direction call publishes a new version we could not read back).
  const versionStale = useRef(false)
  const inflight = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])

  // Compose what would be sent. Invalid or hidden numbers keep the saved value
  // (they are flagged inline and Save is disabled while any is flagged).
  const showDebounce = form.triggers.onChange
  const showPoll = form.triggers.onChange && form.triggers.schedule.kind === 'none'
  const errors: Partial<Record<NumKey | 'name', string>> = {}
  const num = (k: NumKey, fallback: number, visible: boolean, max = Infinity) => {
    const n = parseWhole(text[k])
    if (n !== null && n <= max) return n
    if (visible) errors[k] = max === 100 ? percentMsg : whole
    return fallback
  }
  const name = form.name.trim()
  if (!name) errors.name = 'Enter a name.'
  const edited: Rules = {
    ...form,
    name,
    triggers: {
      ...form.triggers,
      schedule: normalizeSchedule(form.triggers.schedule),
      debounceSeconds: num('debounce', base.rules.triggers.debounceSeconds, showDebounce),
      remotePollMinutes: num('poll', base.rules.triggers.remotePollMinutes, showPoll),
    },
    excludes: splitLines(excludes),
    snapshotRetention: num('retention', base.rules.snapshotRetention, true),
    deleteGuardMinFiles: num('files', base.rules.deleteGuardMinFiles, true),
    deleteGuardPercent: num('percent', base.rules.deleteGuardPercent, true, 100),
  }
  const invalid = Object.keys(errors).length > 0
  const rulesChanged = !same(edited, base.rules)
  const dirChanged = machineDir !== base.machineDir
  const dirty = rulesChanged || dirChanged || invalid
  useDirtyGuard(dirty)

  const load = (rules: Rules, version: string, md: Direction | '') => {
    setBase({ rules, version, machineDir: md })
    setForm(rules)
    setText(toText(rules))
    setExcludes(rules.excludes.join('\n'))
    setMachineDir(md)
  }
  // Follow changes from events while the user has not edited anything.
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const baseRef = useRef(base)
  baseRef.current = base
  useEffect(() => {
    if (!dirtyRef.current && !inflight.current && isNewer(st.action.updatedAt, baseRef.current.version)) {
      load(rulesOf(st.action), st.action.updatedAt, loadedMachineDir)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [st.action.updatedAt])

  const set = <K extends keyof Rules>(k: K, v: Rules[K]) => setForm((f) => ({ ...f, [k]: v }))
  const setTrig = <K extends keyof Rules['triggers']>(k: K, v: Rules['triggers'][K]) => setForm((f) => ({ ...f, triggers: { ...f.triggers, [k]: v } }))
  const setNum = (k: NumKey, v: string) => setText((t) => ({ ...t, [k]: v }))
  const message = (e: unknown) => {
    const d = describeError(e)
    return d.details ? `${d.message} (${d.details})` : d.message
  }

  // currentVersion returns the version to save against. Normally that is the
  // loaded one; after an unreadable direction call it is re-read first, and
  // if the rules changed in the meantime that is a conflict like any other.
  const currentVersion = async (): Promise<string | null> => {
    if (!versionStale.current) return base.version
    try {
      const fresh = await api.action(id)
      if (!alive.current) return null
      if (!same(rulesOf(fresh.action), base.rules)) {
        setConflict(true)
        return null
      }
      versionStale.current = false
      setBase((b) => ({ ...b, version: fresh.action.updatedAt }))
      return fresh.action.updatedAt
    } catch (e) {
      if (alive.current) setError(`Could not read the latest version: ${message(e)}`)
      return null
    }
  }

  const save = async () => {
    if (inflight.current || invalid || !dirty) return
    inflight.current = true
    setBusy(true)
    setError('')
    try {
      let saved: { rules: Rules; version: string } | null = null
      if (rulesChanged) {
        const sched = edited.triggers.schedule
        if (sched.kind !== 'none') {
          try {
            await api.previewSchedule(sched)
          } catch (e) {
            if (alive.current) setError(message(e))
            return
          }
        }
        const version = await currentVersion()
        if (version === null || !alive.current) return
        try {
          const a = await api.updateRules(id, edited, version)
          saved = { rules: rulesOf(a), version: a.updatedAt }
        } catch (e) {
          if (!alive.current) return
          if (toAgentError(e).code === 'conflict-edit') setConflict(true)
          else setError(message(e))
          return
        }
        // The rules are saved from here on, whatever happens next.
        const s = saved
        if (alive.current) setBase((b) => ({ ...b, rules: s.rules, version: s.version }))
      }
      if (dirChanged) {
        try {
          await api.setMachineDirection(id, machineDir)
        } catch (e) {
          // It may or may not have published a new version; re-read before the next rules save.
          versionStale.current = true
          if (alive.current) setError(`${saved ? 'The rules were saved, but this' : 'This'} machine's direction was not changed: ${message(e)}`)
          return
        }
        // Changing the direction publishes a new version of the action: take it as the base.
        try {
          const fresh = await api.action(id)
          if (!alive.current) return
          versionStale.current = false
          load(rulesOf(fresh.action), fresh.action.updatedAt, fresh.action.machineDirections?.[machineId] ?? '')
        } catch (e) {
          versionStale.current = true
          if (!alive.current) return
          setBase((b) => ({ ...b, machineDir }))
          setError(`Saved, but the latest version could not be re-read (${message(e)}). It is re-read on the next save.`)
        }
      } else if (saved) {
        load(saved.rules, saved.version, base.machineDir)
      }
      toast.show('Rules saved.')
    } finally {
      inflight.current = false
      if (alive.current) setBusy(false)
    }
  }

  const reload = async (reapply: boolean) => {
    if (inflight.current) return
    inflight.current = true
    setBusy(true)
    setConflictError('')
    try {
      const fresh = await api.action(id)
      if (!alive.current) return
      const freshRules = rulesOf(fresh.action)
      const freshMd = fresh.action.machineDirections?.[machineId] ?? ''
      versionStale.current = false
      if (!reapply) {
        load(freshRules, fresh.action.updatedAt, freshMd)
      } else {
        const merged = reapplyEdits(base.rules, edited, freshRules)
        setBase({ rules: freshRules, version: fresh.action.updatedAt, machineDir: freshMd })
        setForm(merged)
        setText(toText(merged))
        setExcludes(merged.excludes.join('\n'))
        // Keep my machine direction only if I changed it.
        setMachineDir(dirChanged ? machineDir : freshMd)
      }
      setConflict(false)
    } catch (e) {
      if (alive.current) setConflictError(message(e))
    } finally {
      inflight.current = false
      if (alive.current) setBusy(false)
    }
  }

  return (
    <div className="max-w-2xl space-y-4">
      <Field label="Name" error={errors.name}><Input aria-label="Name" value={form.name} onChange={(e) => set('name', e.target.value)} /></Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Default direction" hint="For every machine without its own setting.">
          <Select aria-label="Default direction" value={form.direction} onChange={(e) => set('direction', e.target.value as Direction)}>
            {(Object.keys(directionLabels) as Direction[]).map((d) => <option key={d} value={d}>{directionLabels[d]}</option>)}
          </Select>
        </Field>
        <Field label="This machine">
          <Select aria-label="This machine" value={machineDir} onChange={(e) => setMachineDir(e.target.value as Direction | '')}>
            <option value="">Use the default</option>
            {(Object.keys(directionLabels) as Direction[]).map((d) => <option key={d} value={d}>{directionLabels[d]}</option>)}
          </Select>
        </Field>
      </div>
      <fieldset className="space-y-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-700">
        <legend className="px-1 text-sm font-medium">Triggers</legend>
        <Checkbox label="Sync when local files change" checked={form.triggers.onChange} onChange={(v) => setTrig('onChange', v)} />
        {showDebounce && (
          <Field label="Wait for changes to settle (seconds)" error={errors.debounce}>
            <Input aria-label="Debounce seconds" type="number" min={1} value={text.debounce} onChange={(e) => setNum('debounce', e.target.value)} />
          </Field>
        )}
        <ScheduleEditor value={form.triggers.schedule} onChange={(s) => setTrig('schedule', s)} />
        {showPoll && (
          <Field label="Check the cloud for changes every (minutes)" error={errors.poll}>
            <Input aria-label="Cloud poll minutes" type="number" min={1} value={text.poll} onChange={(e) => setNum('poll', e.target.value)} />
          </Field>
        )}
      </fieldset>
      <Checkbox label="Copy deletions to the other side (after a safety snapshot)" checked={form.propagateDeletes} onChange={(v) => set('propagateDeletes', v)} />
      {!form.propagateDeletes && (
        <Checkbox label="Cloud-to-local machines upload files the cloud deleted" checked={form.receiverRestoresCloud} onChange={(v) => set('receiverRestoresCloud', v)} />
      )}
      <Field label="Excludes" hint="One glob per line, e.g. *.log or cache/**">
        <textarea aria-label="Excludes" rows={4} value={excludes} onChange={(e) => setExcludes(e.target.value)}
          className="w-full rounded-md border border-zinc-300 bg-white px-2 py-1.5 font-mono text-sm dark:border-zinc-600 dark:bg-zinc-800" />
      </Field>
      <div className="grid grid-cols-3 gap-3">
        <Field label="Manual snapshots to keep" error={errors.retention}>
          <Input aria-label="Manual snapshots to keep" type="number" min={1} value={text.retention} onChange={(e) => setNum('retention', e.target.value)} />
        </Field>
        <Field label="Delete guard: files" error={errors.files}>
          <Input aria-label="Delete guard files" type="number" min={1} value={text.files} onChange={(e) => setNum('files', e.target.value)} />
        </Field>
        <Field label="Delete guard: percent" error={errors.percent}>
          <Input aria-label="Delete guard percent" type="number" min={1} max={100} value={text.percent} onChange={(e) => setNum('percent', e.target.value)} />
        </Field>
      </div>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <div className="flex gap-2">
        <Button variant="primary" disabled={!dirty || invalid || busy} onClick={save}>Save</Button>
        <Button disabled={!dirty || busy} onClick={() => { setError(''); load(base.rules, base.version, base.machineDir) }}>Discard changes</Button>
      </div>
      {conflict && (
        <Modal title="Changed elsewhere" onClose={() => { if (!inflight.current) setConflict(false) }} footer={
          <>
            <Button disabled={busy} onClick={() => reload(false)}>Reload (discard my edits)</Button>
            <Button variant="primary" disabled={busy} onClick={() => reload(true)}>Reload and re-apply my edits</Button>
          </>
        }>
          <p className="text-sm">These rules were changed (maybe on another machine) after you opened them. Nothing was saved.</p>
          {conflictError && <p role="alert" className="mt-2 text-sm text-red-600">{conflictError}</p>}
        </Modal>
      )}
    </div>
  )
}
