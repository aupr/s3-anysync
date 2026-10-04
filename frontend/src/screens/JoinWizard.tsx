import { useEffect, useRef, useState } from 'react'
import { useToast } from '../components/toast'
import { Button, Field, Input, Modal, Select } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { Action } from '../lib/types'

export function JoinWizard({ onClose }: { onClose: () => void }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const [profileId, setProfileId] = useState(state.profiles[0]?.id ?? '')
  const [found, setFound] = useState<Action[] | null>(null)
  const [finding, setFinding] = useState(false)
  const [chosen, setChosen] = useState<Action | null>(null)
  const [pass, setPass] = useState('')
  const [joined, setJoined] = useState<Action | null>(null)
  const [paths, setPaths] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  // Bumped whenever the profile changes, so an older Discover cannot apply.
  const findSeq = useRef(0)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  // A join or mapping request in flight would complete invisibly after a close.
  const close = () => { if (!busy) onClose() }

  const find = async () => {
    const seq = ++findSeq.current
    setError('')
    setFinding(true)
    setPass('')
    try {
      const list = (await api.discover(profileId)).filter((a) => !state.actions[a.id])
      if (alive.current && seq === findSeq.current) { setFound(list); setChosen((c) => (c && list.some((a) => a.id === c.id) ? c : null)) }
    } catch (e) {
      if (alive.current && seq === findSeq.current) setError(describeError(e).message)
    } finally {
      if (alive.current && seq === findSeq.current) setFinding(false)
    }
  }
  const join = async () => {
    if (!chosen || busy) return
    setError('')
    setBusy(true)
    try {
      const a = await api.joinAction({ profileId, actionId: chosen.id, passphrase: chosen.encrypt ? pass : undefined })
      if (alive.current) setJoined(a)
    } catch (e) {
      if (alive.current) setError(describeError(e).message)
    } finally {
      if (alive.current) setBusy(false)
    }
  }
  const pick = async (itemId: string, type: 'file' | 'dir') => {
    setError('')
    try {
      const p = type === 'dir' ? await api.pickFolder('Choose the local folder') : await api.pickFile('Choose the local file')
      if (p && alive.current) setPaths((x) => ({ ...x, [itemId]: p }))
    } catch (e) {
      if (alive.current) setError(describeError(e).message)
    }
  }
  const finish = async () => {
    if (!joined || busy) return
    setBusy(true)
    const failed: string[] = []
    for (const [itemId, p] of Object.entries(paths)) {
      try {
        await api.mapItem(joined.id, itemId, p)
      } catch (e) {
        failed.push(describeError(e).message)
      }
    }
    if (failed.length) toast.show(`Some items were not mapped: ${failed.join('; ')}`, { tone: 'error' })
    if (!alive.current) return
    setBusy(false)
    onClose()
    navigate({ name: 'action', id: joined.id, tab: 'items' })
  }

  // Closing the map step still leaves the action joined, so go to its Items tab.
  const closeMap = () => {
    if (busy || !joined) return
    toast.show('Joined. Map the items on the Items tab to start syncing here.')
    onClose()
    navigate({ name: 'action', id: joined.id, tab: 'items' })
  }

  if (joined) {
    return (
      <Modal title={`Map ${joined.name} on this machine`} onClose={closeMap} footer={<Button variant="primary" disabled={busy} onClick={finish}>Finish</Button>}>
        <p className="mb-2 text-sm text-zinc-500">Choose where each item lives on this machine. Items you skip are not synced here.</p>
        {(joined.items ?? []).map((it) => (
          <div key={it.id} className="mb-2 rounded border border-zinc-200 p-2 text-sm dark:border-zinc-700">
            <div className="font-medium">{it.remoteName} <span className="text-xs text-zinc-500">({it.type})</span></div>
            {Object.entries(it.localPaths ?? {}).map(([m, p]) => <div key={m} className="truncate text-xs text-zinc-500">{joined.machines?.[m] ?? m}: {p}</div>)}
            <div className="mt-1 flex items-center gap-2">
              <Button disabled={busy} onClick={() => pick(it.id, it.type)}>Choose…</Button>
              <span className="truncate text-xs">{paths[it.id] ?? 'not mapped'}</span>
            </div>
          </div>
        ))}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </Modal>
    )
  }

  return (
    <Modal title="Join an existing action" onClose={close} footer={
      <>
        <Button disabled={busy} onClick={close}>Cancel</Button>
        <Button variant="primary" disabled={!chosen || busy || (chosen.encrypt && !pass)} onClick={join}>Join</Button>
      </>
    }>
      <div className="space-y-3">
        {state.profiles.length === 0 && <p className="text-sm text-zinc-500">Create a profile (a bucket connection) first.</p>}
        <div className="flex items-end gap-2">
          <Field label="Profile">
            <Select aria-label="Profile" value={profileId} disabled={busy} onChange={(e) => { findSeq.current++; setProfileId(e.target.value); setFound(null); setChosen(null); setPass(''); setFinding(false); setError('') }}>
              {state.profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </Field>
          <Button disabled={!profileId || finding || busy} onClick={find}>Find actions</Button>
        </div>
        {found && found.length === 0 && <p className="text-sm text-zinc-500">No actions to join in this bucket.</p>}
        {found?.map((a) => (
          <label key={a.id} className="flex items-start gap-2 text-sm">
            <input type="radio" name="join" checked={chosen?.id === a.id} disabled={busy} onChange={() => { if (chosen?.id !== a.id) setPass(''); setChosen(a) }} />
            <span>
              <span className="font-medium">{a.name}</span>
              <span className="block text-xs text-zinc-500">
                {(a.items ?? []).length} item(s) · machines: {Object.values(a.machines ?? {}).join(', ') || '—'}{a.encrypt ? ' · encrypted' : ''}
              </span>
            </span>
          </label>
        ))}
        {chosen?.encrypt && <Field label="Passphrase"><Input aria-label="Passphrase" type="password" value={pass} onChange={(e) => setPass(e.target.value)} /></Field>}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </Modal>
  )
}
