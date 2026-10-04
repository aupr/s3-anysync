import { useEffect, useRef, useState } from 'react'
import { useToast } from '../../components/toast'
import { Button, EmptyState, Input, Modal, Spinner, td, th } from '../../components/ui'
import { api, toAgentError } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { formatTime } from '../../lib/format'
import type { ActionStatus, RestoreTarget, SnapshotInfo } from '../../lib/types'
import { useActionCommands } from '../../lib/useActionCommands'
import { useLoad } from '../../lib/useLoad'

// Every reason internal/engine/snapshot.go can write.
export const reasonLabels: Record<string, string> = {
  manual: 'Manual',
  'auto-pre-sync': 'Before a sync',
  'pre-resolve': 'Before resolving conflicts',
  'pre-restore': 'Before a restore',
}

// How many files of a large snapshot the picker renders at once.
export const PICKER_CAP = 500

// Mirrors the semantics of engine.Runner.Restore: restoring locally only
// rewrites files; the sync baseline is not touched.
const targets: { id: RestoreTarget; label: string; hint: string }[] = [
  { id: 'local', label: 'This machine only', hint: 'Overwrites local files only. The next sync uploads them again (two-way) or undoes the restore (cloud → this machine).' },
  { id: 'cloud', label: 'The cloud only', hint: 'Other machines receive these versions on their next sync.' },
  { id: 'both', label: 'This machine and the cloud', hint: 'Everything goes back to the snapshot and stays there.' },
]

function RestoreDialog({ actionId, snap, onClose }: { actionId: string; snap: SnapshotInfo; onClose: () => void }) {
  const toast = useToast()
  const [selectedOnly, setSelectedOnly] = useState(false)
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [filter, setFilter] = useState('')
  const [to, setTo] = useState<RestoreTarget>('both')
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const all = snap.files ?? []
  const needle = filter.toLowerCase()
  const matching = needle ? all.filter((f) => f.path.toLowerCase().includes(needle)) : all
  const shown = matching.slice(0, PICKER_CAP)
  const toggle = (p: string) => setSelected((s) => {
    const n = new Set(s)
    if (n.has(p)) n.delete(p)
    else n.add(p)
    return n
  })
  const restore = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      // An empty paths list means everything in Go, so "selected only" is never sent empty.
      const r = await api.restore(actionId, { ts: snap.ts, paths: selectedOnly ? [...selected].sort() : undefined, to })
      const skipped = r.skippedUnmapped?.length ? ` ${r.skippedUnmapped.length} file(s) belong to items not mapped here and were skipped locally.` : ''
      toast.show(`Restored ${r.restored?.length ?? 0} file(s).${skipped}`)
      if (alive.current) onClose()
    } catch (e) {
      if (toAgentError(e).code === 'cancelled') toast.show('Restore cancelled.')
      else toast.error(e)
    } finally {
      busyRef.current = false
      if (alive.current) setBusy(false)
    }
  }
  return (
    <Modal title={`Restore snapshot from ${formatTime(snap.createdAt)}`} wide onClose={busy ? () => {} : onClose} footer={
      <><Button disabled={busy} onClick={onClose}>Cancel</Button><Button variant="primary" disabled={busy || all.length === 0 || (selectedOnly && selected.size === 0)} onClick={restore}>Restore</Button></>
    }>
      <div className="space-y-3 text-sm">
        <p>The current state is snapshotted first, so a restore can be undone. Restoring never deletes files.</p>
        {all.length === 0 && <p role="alert" className="text-red-600">This snapshot has no files to restore.</p>}
        <div className="space-y-1">
          <label className="flex items-center gap-2"><input type="radio" name="scope" disabled={busy} checked={!selectedOnly} onChange={() => setSelectedOnly(false)} />All {all.length} files</label>
          <label className="flex items-center gap-2"><input type="radio" name="scope" disabled={busy} checked={selectedOnly} onChange={() => setSelectedOnly(true)} />Selected files only</label>
        </div>
        {selectedOnly && (
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <Input aria-label="Filter files" placeholder="Filter…" value={filter} disabled={busy} onChange={(e) => setFilter(e.target.value)} />
              <Button disabled={busy || matching.length === 0} onClick={() => setSelected((s) => new Set([...s, ...matching.map((f) => f.path)]))}>Select all matching</Button>
              <Button disabled={busy || selected.size === 0} onClick={() => setSelected(new Set())}>Clear</Button>
            </div>
            <div className="max-h-56 overflow-auto rounded border border-zinc-200 p-2 dark:border-zinc-700">
              {shown.map((f) => (
                <label key={f.path} className="flex items-center gap-2"><input type="checkbox" disabled={busy} checked={selected.has(f.path)} onChange={() => toggle(f.path)} />{f.path}</label>
              ))}
              {matching.length > shown.length && <p className="pt-1 text-xs text-zinc-500">{matching.length - shown.length} more — narrow the filter to see them.</p>}
              {matching.length === 0 && <p className="text-xs text-zinc-500">No file matches the filter.</p>}
            </div>
            <p className="text-xs text-zinc-500">{selected.size} selected</p>
          </div>
        )}
        <div className="space-y-1">
          {targets.map((t) => (
            <label key={t.id} className="flex items-start gap-2">
              <input type="radio" name="target" aria-label={t.label} disabled={busy} checked={to === t.id} onChange={() => setTo(t.id)} />
              <span>{t.label}<span className="block text-xs text-zinc-500">{t.hint}</span></span>
            </label>
          ))}
        </div>
        {to !== 'cloud' && <p className="text-xs text-zinc-500">Files of items that are not mapped on this machine are skipped locally.</p>}
      </div>
    </Modal>
  )
}

export function SnapshotsTab({ st }: { st: ActionStatus }) {
  const cmd = useActionCommands()
  const id = st.action.id
  const [restoring, setRestoring] = useState<SnapshotInfo | null>(null)
  const [taking, setTaking] = useState(false)
  const takingRef = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const { data, error, reload } = useLoad(() => api.snapshots(id), (e) => {
    const d = describeError(e)
    return d.details ? `${d.message} (${d.details})` : d.message
  }, [id, st.lastRun])
  // Timestamps are fixed-width UTC, so they sort as text; newest first.
  const list = data && [...data].sort((a, b) => b.ts.localeCompare(a.ts))

  const take = async () => {
    if (takingRef.current) return
    takingRef.current = true
    setTaking(true)
    try {
      await cmd.snapshot(id)
      reload()
    } finally {
      takingRef.current = false
      if (alive.current) setTaking(false)
    }
  }
  const problem = error && (
    <div role="alert" className="flex items-center gap-3 text-sm text-red-600">
      <span>Could not load the snapshots: {error}</span>
      <Button onClick={reload}>Retry</Button>
    </div>
  )
  return (
    <div className="space-y-3">
      <Button variant="primary" disabled={st.running || taking} onClick={take}>Snapshot now</Button>
      {problem}
      {list === null ? (error ? null : <Spinner />) : list.length === 0 ? <EmptyState title="No snapshots yet" /> : (
        <table className="w-full">
          <thead><tr><th className={th}>Taken</th><th className={th}>Reason</th><th className={th}>Machine</th><th className={th}>Files</th><th className={th} /></tr></thead>
          <tbody>
            {list.map((s) => (
              <tr key={s.ts} className="border-t border-zinc-200 dark:border-zinc-800">
                <td className={td}>{formatTime(s.createdAt)}</td>
                <td className={td}>{reasonLabels[s.reason] ?? s.reason}{s.deletes?.length ? <div className="text-xs text-zinc-500">before deleting {s.deletes.length} file(s)</div> : null}</td>
                <td className={td}>{st.action.machines?.[s.machineId] ?? s.machineId}</td>
                <td className={td}>{s.files?.length ?? 0}</td>
                <td className={`${td} text-right`}><Button aria-label={`Restore snapshot ${formatTime(s.createdAt)}`} onClick={() => setRestoring(s)}>Restore…</Button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {restoring && <RestoreDialog actionId={id} snap={restoring} onClose={() => { setRestoring(null); reload() }} />}
    </div>
  )
}
