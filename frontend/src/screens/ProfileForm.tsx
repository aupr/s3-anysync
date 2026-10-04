import { useState } from 'react'
import { Button, Checkbox, Field, Input, Modal, Select } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { providers } from '../lib/providers'
import { useDirtyGuard } from '../lib/router'
import type { ProbeResult, Profile } from '../lib/types'

const blank: Profile = { id: '', name: '', provider: 'aws', endpoint: '', region: 'us-east-1', bucket: '', rootPrefix: '', accessKeyId: '', pathStyle: false, conditionalWrites: false }

export function ProbeChecklist({ result }: { result: ProbeResult }) {
  const row = (label: string, ok: boolean) => (
    <li key={label} className={ok ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}>
      {ok ? '✓' : '✗'} {label}
    </li>
  )
  return (
    <div className="space-y-1 text-sm">
      <ul>{[row('List', result.list), row('Upload', result.put), row('Download', result.get), row('Delete', result.delete)]}</ul>
      <p>
        Conditional writes: {result.conditionalWrites ? 'yes (safe cloud lock)' : 'no (best-effort cloud lock; avoid syncing the same action from two machines at the same moment)'}
      </p>
    </div>
  )
}

export function ProfileForm({ profile, onSaved, onCancel }: { profile?: Profile; onSaved: (p: Profile) => void; onCancel: () => void }) {
  const editing = !!profile
  const [p, setP] = useState<Profile>(profile ?? blank)
  const [secret, setSecret] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const [saved, setSaved] = useState<Profile | null>(null)
  const dirty = !saved && (JSON.stringify(p) !== JSON.stringify(profile ?? blank) || secret !== '')
  useDirtyGuard(dirty)
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }))

  const pickProvider = (id: string) => {
    const pre = providers.find((x) => x.id === id)!
    setP((x) => ({ ...x, provider: id, endpoint: pre.endpoint, region: pre.region, pathStyle: pre.pathStyle }))
  }

  const save = async () => {
    setError('')
    if (!p.name.trim() || !p.bucket.trim() || !p.accessKeyId.trim()) return setError('Name, bucket and access key ID are required.')
    if (!editing && !secret) return setError('The secret access key is required.')
    if (p.endpoint.includes('<') || p.region.includes('<')) return setError('Replace the <…> parts of the endpoint and region.')
    setBusy(true)
    try {
      const out = editing ? await api.updateProfile(p, secret) : await api.addProfile(p, secret)
      setSaved(out)
      if (!editing) setProbe(await api.testProfile(out.id).catch((e) => { setError(describeError(e).message); return null }))
      else onSaved(out)
    } catch (e) {
      const d = describeError(e)
      setError(d.details ? `${d.message} (${d.details})` : d.message)
    } finally {
      setBusy(false)
    }
  }

  if (saved && !editing) {
    return (
      <Modal title="Profile saved" onClose={() => onSaved(saved)} footer={<Button variant="primary" onClick={() => onSaved(saved)}>Done</Button>}>
        {probe ? <ProbeChecklist result={probe} /> : <p className="text-sm">Testing the connection…</p>}
        {error && <p role="alert" className="mt-2 text-sm text-red-600">{error}</p>}
      </Modal>
    )
  }

  const preset = providers.find((x) => x.id === p.provider)
  return (
    <Modal title={editing ? `Edit ${profile!.name}` : 'New profile'} onClose={onCancel} footer={
      <>
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="primary" disabled={busy} onClick={save}>Save</Button>
      </>
    }>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Name"><Input aria-label="Name" value={p.name} onChange={(e) => set('name', e.target.value)} /></Field>
        <Field label="Provider">
          <Select aria-label="Provider" value={p.provider} onChange={(e) => pickProvider(e.target.value)}>
            {providers.map((x) => <option key={x.id} value={x.id}>{x.label}</option>)}
          </Select>
        </Field>
        <Field label="Endpoint" hint={preset?.hint}><Input aria-label="Endpoint" value={p.endpoint} onChange={(e) => set('endpoint', e.target.value)} /></Field>
        <Field label="Region"><Input aria-label="Region" value={p.region} onChange={(e) => set('region', e.target.value)} /></Field>
        <Field label="Bucket"><Input aria-label="Bucket" value={p.bucket} onChange={(e) => set('bucket', e.target.value)} /></Field>
        <Field label="Folder in the bucket" hint="Optional."><Input aria-label="Folder in the bucket" value={p.rootPrefix} onChange={(e) => set('rootPrefix', e.target.value)} /></Field>
        <Field label="Access key ID"><Input aria-label="Access key ID" value={p.accessKeyId} onChange={(e) => set('accessKeyId', e.target.value)} /></Field>
        <Field label="Secret access key" hint={editing ? 'Leave empty to keep the stored secret.' : 'Stored in the system keychain.'}>
          <Input aria-label="Secret access key" type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} />
        </Field>
      </div>
      <div className="mt-3">
        <Checkbox label="Path-style addressing" checked={p.pathStyle} onChange={(v) => set('pathStyle', v)} />
      </div>
      {error && <p role="alert" className="mt-3 text-sm text-red-600">{error}</p>}
    </Modal>
  )
}
