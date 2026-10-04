import { useEffect, useRef, useState } from 'react'
import { useToast } from '../components/toast'
import { Button, Checkbox, Field, Input, Select, Spinner } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { useDirtyGuard } from '../lib/router'
import { useStore } from '../lib/store'
import type { Settings } from '../lib/types'
import { useLoad } from '../lib/useLoad'

// The values internal/logging ParseLevel accepts.
const levels = ['debug', 'info', 'warn', 'error']

const describe = (e: unknown) => describeError(e).message
const same = (a: Settings, b: Settings) => a.machineName === b.machineName && a.autostart === b.autostart && a.logLevel === b.logLevel

export function SettingsScreen() {
  const { state, refresh } = useStore()
  const toast = useToast()
  const { data: fetched, error: loadError, reload } = useLoad(() => api.settings(), describe, [])
  const { data: dir } = useLoad(() => api.dataDir(), describe, [])
  // saved: the value the agent returned for the last successful save;
  // edit: the unsaved form, null while nothing was changed.
  const [saved, setSaved] = useState<Settings | null>(null)
  const [edit, setEdit] = useState<Settings | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const base = saved ?? fetched
  const form = edit ?? base
  const dirty = !!edit && !!base && !same(edit, base)
  useDirtyGuard(dirty)
  if (!base || !form) {
    if (loadError) {
      return (
        <div className="max-w-xl space-y-3">
          <h1 className="text-xl font-semibold">Settings</h1>
          <p role="alert" className="text-sm text-red-600">{loadError}</p>
          <Button onClick={reload}>Retry</Button>
        </div>
      )
    }
    return <Spinner />
  }
  const change = (patch: Partial<Settings>) => setEdit({ ...form, ...patch })
  const save = async () => {
    if (inFlight.current) return
    if (form.machineName.trim() === '') {
      setError('The machine name cannot be empty.')
      return
    }
    inFlight.current = true
    setBusy(true)
    setError('')
    try {
      const s = await api.updateSettings(form)
      if (!alive.current) return
      setSaved(s)
      setEdit(null)
      toast.show('Settings saved.')
      refresh().catch(() => {})
    } catch (e) {
      if (!alive.current) return
      setError(describeError(e).message)
      // The agent did not enable the login item: show what is really set.
      if (form.autostart !== base.autostart) setEdit({ ...form, autostart: base.autostart })
    } finally {
      inFlight.current = false
      if (alive.current) setBusy(false)
    }
  }
  const options = levels.includes(form.logLevel) ? levels : [form.logLevel, ...levels]
  return (
    <div className="max-w-xl space-y-4">
      <h1 className="text-xl font-semibold">Settings</h1>
      <fieldset disabled={busy} className="space-y-4">
        <Field label="Machine name" hint="Shown to the other machines of your actions.">
          <Input aria-label="Machine name" value={form.machineName} onChange={(e) => change({ machineName: e.target.value })} />
        </Field>
        {form.autostartSupported && (
          <div>
            <Checkbox label="Start the agent when I log in" checked={form.autostart} onChange={(v) => change({ autostart: v })} />
            <p className="mt-1 text-xs text-zinc-500">Registers this app to start at login for your user account.</p>
          </div>
        )}
        <Field label="Log level">
          <Select aria-label="Log level" value={form.logLevel} onChange={(e) => change({ logLevel: e.target.value })}>
            {options.map((l) => <option key={l} value={l}>{l}</option>)}
          </Select>
        </Field>
      </fieldset>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <Button variant="primary" disabled={busy} onClick={save}>{busy ? 'Saving…' : 'Save'}</Button>
      <dl className="grid grid-cols-[10rem_1fr] gap-y-1 border-t border-zinc-200 pt-4 text-sm dark:border-zinc-800">
        <dt className="text-zinc-500">Machine ID</dt><dd className="font-mono text-xs">{form.machineId}</dd>
        <dt className="text-zinc-500">Agent version</dt><dd>{state.status?.version ?? '—'}</dd>
        <dt className="text-zinc-500">Data folder</dt>
        <dd>
          <span className="break-all">{dir ?? '—'}</span>{' '}
          <Button variant="ghost" aria-label="Open data folder" onClick={() => api.openDataFolder().catch(toast.error)}>Open</Button>
        </dd>
      </dl>
    </div>
  )
}
