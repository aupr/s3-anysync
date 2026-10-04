import { useEffect, useRef, useState } from 'react'
import { ScheduleEditor } from '../components/ScheduleEditor'
import { useToast } from '../components/toast'
import { Button, Checkbox, Field, Input, Modal, Select } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { directionLabels } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { Direction, Schedule } from '../lib/types'

export const basename = (p: string) => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? p

export interface DraftItem { remoteName: string; type: 'file' | 'dir'; localPath: string; excludes: string }

export const splitLines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)

const steps = ['Profile', 'Rules', 'Encryption', 'Items'] as const

export function NewActionWizard({ onClose }: { onClose: () => void }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const [step, setStep] = useState(0)
  const [profileId, setProfileId] = useState(state.profiles[0]?.id ?? '')
  const [name, setName] = useState('')
  const [direction, setDirection] = useState<Direction>('two-way')
  const [onChange, setOnChange] = useState(true)
  const [schedule, setSchedule] = useState<Schedule>({ kind: 'none' })
  const [encrypt, setEncrypt] = useState(false)
  const [pass, setPass] = useState('')
  const [pass2, setPass2] = useState('')
  const [items, setItems] = useState<DraftItem[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  const checkSeq = useRef(0)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  // A request in flight would complete invisibly after a close, so closing waits.
  const close = () => { if (!busy) onClose() }

  if (state.profiles.length === 0) {
    return (
      <Modal title="New action" onClose={onClose} footer={<Button variant="primary" onClick={() => { onClose(); navigate({ name: 'profiles' }) }}>Go to Profiles</Button>}>
        <p className="text-sm">Create a profile (a bucket connection) first.</p>
      </Modal>
    )
  }

  const next = async () => {
    setError('')
    if (step === 1 && !name.trim()) return setError('Give the action a name.')
    if (step === 1 && schedule.kind !== 'none') {
      const seq = ++checkSeq.current
      try {
        await api.previewSchedule(schedule)
      } catch (e) {
        if (alive.current && seq === checkSeq.current) setError(describeError(e).message)
        return
      }
      if (!alive.current || seq !== checkSeq.current) return
    }
    if (step === 2 && encrypt && (pass.length < 8 || pass !== pass2)) return setError('The passphrase must have at least 8 characters and both entries must match.')
    setStep(step + 1)
  }
  const pick = async (type: 'file' | 'dir') => {
    setError('')
    try {
      const p = type === 'dir' ? await api.pickFolder('Choose a folder to sync') : await api.pickFile('Choose a file to sync')
      if (p && alive.current) setItems((x) => [...x, { remoteName: basename(p), type, localPath: p, excludes: '' }])
    } catch (e) {
      if (alive.current) setError(describeError(e).message)
    }
  }
  const create = async () => {
    if (busy) return
    setError('')
    setBusy(true)
    try {
      const act = await api.createAction({
        profileId,
        action: { name: name.trim(), direction, propagateDeletes: true, ...(encrypt ? { encrypt: true } : {}), triggers: { onChange, debounceSeconds: 0, schedule, remotePollMinutes: 0 } },
        passphrase: encrypt ? pass : undefined,
      })
      const failed: string[] = []
      for (const it of items) {
        try {
          await api.addItem(act.id, { remoteName: it.remoteName, type: it.type, excludes: splitLines(it.excludes) }, it.localPath)
        } catch (e) {
          failed.push(`${it.remoteName}: ${describeError(e).message}`)
        }
      }
      if (failed.length) toast.show(`The action was created, but some items were not added: ${failed.join('; ')}`, { tone: 'error' })
      if (!alive.current) return
      onClose()
      navigate({ name: 'action', id: act.id, tab: failed.length ? 'items' : 'overview' })
    } catch (e) {
      if (alive.current) setError(describeError(e).message)
    } finally {
      if (alive.current) setBusy(false)
    }
  }

  return (
    <Modal title={`New action — ${steps[step]}`} onClose={close} footer={
      <>
        <Button disabled={busy} onClick={close}>Cancel</Button>
        {step > 0 && <Button disabled={busy} onClick={() => { setError(''); setStep(step - 1) }}>Back</Button>}
        {step < steps.length - 1 ? <Button variant="primary" onClick={next}>Next</Button> : <Button variant="primary" disabled={busy} onClick={create}>Create</Button>}
      </>
    }>
      <div className="space-y-3">
        {step === 0 && (
          <Field label="Profile">
            <Select aria-label="Profile" value={profileId} onChange={(e) => setProfileId(e.target.value)}>
              {state.profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </Field>
        )}
        {step === 1 && (
          <>
            <Field label="Name"><Input aria-label="Name" value={name} onChange={(e) => setName(e.target.value)} /></Field>
            <Field label="Direction">
              <Select aria-label="Direction" value={direction} onChange={(e) => setDirection(e.target.value as Direction)}>
                {(Object.keys(directionLabels) as Direction[]).map((d) => <option key={d} value={d}>{directionLabels[d]}</option>)}
              </Select>
            </Field>
            <Checkbox label="Sync when local files change" checked={onChange} onChange={setOnChange} />
            <ScheduleEditor value={schedule} onChange={setSchedule} />
          </>
        )}
        {step === 2 && (
          <>
            <Checkbox label="Encrypt files in the bucket" checked={encrypt} onChange={setEncrypt} />
            {encrypt && (
              <>
                <p className="text-sm text-amber-700">Encryption cannot be changed later, and a lost passphrase cannot be recovered. Every machine that joins needs it.</p>
                <Field label="Passphrase"><Input aria-label="Passphrase" type="password" value={pass} onChange={(e) => setPass(e.target.value)} /></Field>
                <Field label="Repeat passphrase"><Input aria-label="Repeat passphrase" type="password" value={pass2} onChange={(e) => setPass2(e.target.value)} /></Field>
              </>
            )}
          </>
        )}
        {step === 3 && (
          <>
            <p className="text-sm text-zinc-500">Optional: add the files or folders to sync now. You can add more later.</p>
            {items.map((it, i) => (
              <div key={i} className="rounded border border-zinc-200 p-2 dark:border-zinc-700">
                <div className="truncate text-xs text-zinc-500">{it.localPath}</div>
                <Field label="Name in the bucket">
                  <Input aria-label="Name in the bucket" value={it.remoteName} onChange={(e) => setItems((x) => x.map((y, j) => (j === i ? { ...y, remoteName: e.target.value } : y)))} />
                </Field>
                <Button variant="ghost" disabled={busy} onClick={() => setItems((x) => x.filter((_, j) => j !== i))}>Remove</Button>
              </div>
            ))}
            <div className="space-x-2">
              <Button disabled={busy} onClick={() => pick('dir')}>Add folder</Button>
              <Button disabled={busy} onClick={() => pick('file')}>Add file</Button>
            </div>
          </>
        )}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </Modal>
  )
}
