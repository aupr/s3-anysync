import { useEffect, useRef, useState } from 'react'
import { useToast } from '../../components/toast'
import { Button, ConfirmDialog, EmptyState, Field, Input, Modal, Select, Spinner, td, th } from '../../components/ui'
import { api } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { useStore } from '../../lib/store'
import type { ActionStatus, Item } from '../../lib/types'
import { basename, splitLines } from '../NewActionWizard'

// normalizeRemoteName mirrors model.NormalizeRemoteName in Go: backslashes
// become slashes, surrounding slashes are trimmed and "." / ".." are
// resolved. It returns null for a name the agent would refuse.
export function normalizeRemoteName(s: string): string | null {
  const parts: string[] = []
  for (const seg of s.trim().replace(/\\/g, '/').split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (parts.length === 0) return null
      parts.pop()
    } else parts.push(seg)
  }
  return parts.length ? parts.join('/') : null
}

// Mirrors the overlap rule of model.Action.Validate.
const overlaps = (a: string, b: string) => a === b || a.startsWith(`${b}/`) || b.startsWith(`${a}/`)

function AddItemDialog({ actionId, existing, onClose }: { actionId: string; existing: string[]; onClose: () => void }) {
  const [type, setType] = useState<'file' | 'dir'>('dir')
  const [localPath, setLocalPath] = useState('')
  const [remoteName, setRemoteName] = useState('')
  const [excludes, setExcludes] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const alive = useRef(true)
  // Bumped whenever the type changes, so a picker answer for the old type is dropped.
  const pickGen = useRef(0)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])

  const normalized = normalizeRemoteName(remoteName)
  let nameProblem = ''
  if (remoteName.trim()) {
    if (normalized === null) nameProblem = 'This name is not allowed: it is empty or leaves the action folder.'
    else {
      const clash = existing.find((e) => overlaps(e, normalized))
      if (clash) nameProblem = `The name overlaps the existing item "${clash}". Items cannot be the same as, or inside, one another.`
    }
  }
  const choose = async () => {
    const gen = pickGen.current
    try {
      const p = type === 'dir' ? await api.pickFolder('Choose a folder to sync') : await api.pickFile('Choose a file to sync')
      if (!alive.current || gen !== pickGen.current || !p) return
      setLocalPath(p)
      setRemoteName((n) => n || basename(p))
    } catch (e) {
      if (alive.current && gen === pickGen.current) setError(describeMessage(e))
    }
  }
  const add = async () => {
    if (busyRef.current || normalized === null || nameProblem) return
    busyRef.current = true
    setError('')
    setBusy(true)
    try {
      await api.addItem(actionId, { remoteName: normalized, type, excludes: type === 'dir' ? splitLines(excludes) : [] }, localPath)
      if (alive.current) onClose()
    } catch (e) {
      if (alive.current) setError(describeMessage(e))
    } finally {
      busyRef.current = false
      if (alive.current) setBusy(false)
    }
  }
  return (
    <Modal title="Add item" onClose={busy ? () => {} : onClose} footer={<><Button disabled={busy} onClick={onClose}>Cancel</Button><Button variant="primary" disabled={busy || !localPath || normalized === null || !!nameProblem} onClick={add}>Add</Button></>}>
      <div className="space-y-3">
        <Field label="Type">
          <Select aria-label="Type" value={type} disabled={busy} onChange={(e) => { pickGen.current++; setType(e.target.value as 'file' | 'dir'); setLocalPath('') }}>
            <option value="dir">Folder</option>
            <option value="file">File</option>
          </Select>
        </Field>
        <div className="flex items-center gap-2"><Button disabled={busy} onClick={choose}>Choose…</Button><span className="truncate text-xs">{localPath || 'nothing chosen'}</span></div>
        <Field label="Name in the bucket" hint="The same name on every machine; each machine maps it to its own path.">
          <Input aria-label="Name in the bucket" value={remoteName} disabled={busy} onChange={(e) => setRemoteName(e.target.value)} />
        </Field>
        {normalized !== null && normalized !== remoteName.trim() && !nameProblem && <p className="text-xs text-zinc-500">It will be stored as "{normalized}".</p>}
        {nameProblem && <p role="alert" className="text-sm text-red-600">{nameProblem}</p>}
        {type === 'dir' && (
          <Field label="Excludes" hint="One glob per line.">
            <textarea aria-label="Excludes" rows={3} value={excludes} disabled={busy} onChange={(e) => setExcludes(e.target.value)}
              className="w-full rounded-md border border-zinc-300 bg-white px-2 py-1.5 font-mono text-sm dark:border-zinc-600 dark:bg-zinc-800" />
          </Field>
        )}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </Modal>
  )
}

function describeMessage(e: unknown): string {
  const d = describeError(e)
  return d.details ? `${d.message} (${d.details})` : d.message
}

export function ItemsTab({ st }: { st: ActionStatus }) {
  const { state } = useStore()
  const toast = useToast()
  const machineId = state.status?.machineId
  const id = st.action.id
  const [adding, setAdding] = useState(false)
  const [confirm, setConfirm] = useState<{ item: Item; op: 'unmap' | 'remove' } | null>(null)
  const [confirmBusy, setConfirmBusy] = useState(false)
  const confirmRef = useRef(false)
  // Items whose picker or map request is running; their Map button is disabled.
  const [mapping, setMapping] = useState<ReadonlySet<string>>(new Set())
  const mappingRef = useRef(new Set<string>())
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const items = st.action.items ?? []

  const map = async (it: Item) => {
    if (mappingRef.current.has(it.id)) return
    mappingRef.current.add(it.id)
    setMapping((s) => new Set(s).add(it.id))
    try {
      const title = `Where is "${it.remoteName}" on this machine?`
      const p = it.type === 'dir' ? await api.pickFolder(title) : await api.pickFile(title)
      if (p) await api.mapItem(id, it.id, p)
    } catch (e) {
      toast.error(e)
    } finally {
      mappingRef.current.delete(it.id)
      if (alive.current) setMapping((s) => { const n = new Set(s); n.delete(it.id); return n })
    }
  }
  const run = async () => {
    if (!confirm || confirmRef.current) return
    const { item, op } = confirm
    confirmRef.current = true
    setConfirmBusy(true)
    try {
      await (op === 'unmap' ? api.unmapItem(id, item.id) : api.removeItem(id, item.id))
    } catch (e) {
      toast.error(e)
    } finally {
      confirmRef.current = false
      if (alive.current) {
        setConfirmBusy(false)
        setConfirm(null)
      }
    }
  }

  // Without the machine id every item would look unmapped.
  if (machineId === undefined) return <Spinner />

  return (
    <div className="space-y-3">
      <Button variant="primary" onClick={() => setAdding(true)}>Add item</Button>
      {items.length === 0 ? <EmptyState title="No items yet" /> : (
        <table className="w-full">
          <thead><tr><th className={th}>Item</th><th className={th}>On this machine</th><th className={th}>Other machines</th><th className={th} /></tr></thead>
          <tbody>
            {items.map((it) => {
              const here = it.localPaths?.[machineId]
              const others = Object.entries(it.localPaths ?? {}).filter(([m]) => m !== machineId)
              return (
                <tr key={it.id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className={td}><span className="font-medium">{it.remoteName}</span><div className="text-xs text-zinc-500">{it.type === 'dir' ? 'folder' : 'file'}{it.excludes?.length ? ` · excludes: ${it.excludes.join(', ')}` : ''}</div></td>
                  <td className={`${td} break-all`}>{here ?? <span className="text-zinc-500">Not mapped on this machine</span>}</td>
                  <td className={`${td} break-all text-xs text-zinc-500`}>{others.map(([m, p]) => <div key={m}>{st.action.machines?.[m] ?? m}: {p}</div>)}</td>
                  <td className={`${td} space-x-2 whitespace-nowrap text-right`}>
                    <Button disabled={mapping.has(it.id)} aria-label={`${here ? 'Remap' : 'Map'} ${it.remoteName}`} onClick={() => map(it)}>{here ? 'Remap…' : 'Map…'}</Button>
                    {here && <Button aria-label={`Unmap ${it.remoteName}`} onClick={() => setConfirm({ item: it, op: 'unmap' })}>Unmap</Button>}
                    <Button variant="danger" aria-label={`Remove ${it.remoteName}`} onClick={() => setConfirm({ item: it, op: 'remove' })}>Remove</Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {adding && <AddItemDialog actionId={id} existing={items.map((i) => i.remoteName)} onClose={() => setAdding(false)} />}
      {confirm && (
        <ConfirmDialog
          busy={confirmBusy}
          title={confirm.op === 'unmap' ? `Stop syncing ${confirm.item.remoteName} here?` : `Remove ${confirm.item.remoteName}?`}
          danger={confirm.op === 'remove'}
          confirmLabel={confirm.op === 'unmap' ? 'Unmap' : 'Remove'}
          message={confirm.op === 'unmap'
            ? 'This machine stops syncing the item. Local files and the cloud copy stay; other machines keep syncing. If you map it again later, the first sync compares both sides afresh.'
            : 'The item is removed from the action on every machine. Local files and the cloud copies stay in place, but the cloud copies are no longer synced.'}
          onCancel={() => setConfirm(null)} onConfirm={run} />
      )}
    </div>
  )
}
