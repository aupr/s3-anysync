import { useEffect, useRef, useState } from 'react'
import { DiffView } from '../components/DiffView'
import { useToast } from '../components/toast'
import { Button, ConfirmDialog, EmptyState, Spinner } from '../components/ui'
import { api, toAgentError } from '../lib/api'
import { allChosen, deletionsFor, describeEntry } from '../lib/conflicts'
import { describeError } from '../lib/errors'
import { summarizeResult } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { ConflictDetail, Side } from '../lib/types'
import { useLoad } from '../lib/useLoad'

interface DetailState { path: string; detail?: ConflictDetail; error?: string }

export function Conflicts({ id }: { id: string }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const { data: list, error: loadError, reload } = useLoad(() => api.conflicts(id).then((l) => [...l]), (e) => describeError(e).message, [id])
  // One rule: choices are valid only for the exact list object they were made
  // against. After a Resolve that did not succeed, the list on screen is
  // invalid (stale) until a reload delivers a new list object (useLoad's fetch
  // copies the array, so every load is a new object).
  const [choiceState, setChoiceState] = useState<{ list: unknown; map: Record<string, Side> }>({ list: null, map: {} })
  const [staleList, setStaleList] = useState<unknown>(null)
  const stale = list !== null && list === staleList
  const choices = choiceState.list === list && !stale ? choiceState.map : {}
  const pendingDeferred = useRef<{ deferred: string[]; summary: string } | null>(null)
  const setChoices = (f: (m: Record<string, Side>) => Record<string, Side>) => setChoiceState({ list, map: f(choices) })
  const [openState, setOpen] = useState<string | null>(null)
  const open = list?.some((c) => c.path === openState) ? openState : null
  const [ds, setDs] = useState<DetailState | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  // The "deferred" toast is composed once the reload has finished (or failed),
  // so it only counts files that are really still in the list.
  useEffect(() => {
    const p = pendingDeferred.current
    if (!p || (list === staleList && !loadError)) return
    pendingDeferred.current = null
    const inList = new Set((list ?? []).map((c) => c.path))
    const still = p.deferred.filter((x) => inList.has(x)).length
    toast.show(still > 0
      ? `Resolved, but ${still} file(s) changed meanwhile and stay in the list: ${p.summary}`
      : `Resolved; ${p.deferred.length} file(s) changed again meanwhile and will be handled by the next sync.`)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list, loadError])
  const name = state.actions[id]?.action.name ?? id
  const back = () => navigate({ name: 'action', id, tab: 'overview' })

  // Only the opened file is diffed. A response for a file that is no longer
  // open (or after unmount) is dropped by the cleanup.
  useEffect(() => {
    if (!open) return
    let current = true
    api.conflictDetail(id, open).then(
      (detail) => { if (current) setDs({ path: open, detail }) },
      (e: unknown) => { if (current) setDs({ path: open, error: toAgentError(e).message }) },
    )
    return () => { current = false }
  }, [id, open, list])

  if (list === null) {
    return loadError
      ? <div className="space-y-2"><p role="alert" className="text-sm text-red-600">Could not load the conflicts: {loadError}</p><Button onClick={reload}>Retry</Button></div>
      : <Spinner />
  }
  if (list.length === 0) {
    return <EmptyState title="No conflicts"><Button onClick={back}>Back to {name}</Button></EmptyState>
  }
  const deletions = deletionsFor(list, choices)
  const all = (side: Side) => setChoiceState({ list, map: Object.fromEntries(list.map((c) => [c.path, side])) })
  const resolve = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      const r = await api.resolve(id, choices)
      if (r.outcome !== 'ok') setStaleList(list)
      if (r.outcome === 'conflict') {
        toast.show('New conflicts appeared while resolving; review them again.')
        reload()
      } else if (r.outcome === 'deletes-pending') {
        toast.show('Your choices were not applied: other deletions were held back by the delete guard. Review them first.')
        if (alive.current) navigate({ name: 'deletes', id })
      } else if (r.deferred?.length) {
        setStaleList(list)
        pendingDeferred.current = { deferred: r.deferred, summary: summarizeResult(r) }
        reload()
      } else {
        toast.show(`Resolved: ${summarizeResult(r)}`)
        if (alive.current) back()
      }
    } catch (e) {
      const code = toAgentError(e).code
      if (code === 'invalid' || code === 'not-found') {
        // The saved conflicts changed since the user looked: the choices no longer fit.
        setStaleList(list)
        toast.show('The conflicts changed since you looked. Review them again.', { details: toAgentError(e).message, tone: 'error' })
        reload()
      } else {
        toast.error(e)
      }
    } finally {
      busyRef.current = false
      if (alive.current) {
        setBusy(false)
        setConfirm(false)
      }
    }
  }

  const shown = ds?.path === open ? ds : null
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Conflicts in {name}</h1>
        <div className="space-x-2">
          <Button disabled={busy || stale} onClick={() => all('local')}>Keep all local</Button>
          <Button disabled={busy || stale} onClick={() => all('cloud')}>Keep all cloud</Button>
          <Button variant="primary" disabled={stale || !allChosen(list, choices) || busy} onClick={() => (deletions.length ? setConfirm(true) : resolve())}>Apply</Button>
        </div>
      </div>
      {loadError && <div className="space-y-2"><p role="alert" className="text-sm text-red-600">Could not refresh the conflicts: {loadError}</p><Button onClick={reload}>Retry</Button></div>}
      {stale && !loadError && <p role="status" className="text-sm text-zinc-500">Reloading the conflicts…</p>}
      <p className="text-sm text-zinc-500">
        Each file changed on this machine and in the cloud. Choose the version to keep. The version you do not keep is saved first:
        the cloud version in a snapshot, the local version in a local backup on this machine.
      </p>
      <ul className="divide-y divide-zinc-200 rounded border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
        {list.map((c) => (
          <li key={c.path} className="p-3">
            <div className="flex items-center justify-between gap-3">
              <button type="button" className="truncate text-left font-mono text-sm hover:underline" onClick={() => setOpen(open === c.path ? null : c.path)}>{c.path}</button>
              <div role="radiogroup" aria-label={'Choose version for ' + c.path} className="flex shrink-0 gap-3 text-sm">
                <label className="flex items-center gap-1"><input type="radio" name={c.path} aria-label={`Keep local ${c.path}`} disabled={busy || stale} checked={choices[c.path] === 'local'} onChange={() => setChoices((x) => ({ ...x, [c.path]: 'local' }))} />Keep local</label>
                <label className="flex items-center gap-1"><input type="radio" name={c.path} aria-label={`Keep cloud ${c.path}`} disabled={busy || stale} checked={choices[c.path] === 'cloud'} onChange={() => setChoices((x) => ({ ...x, [c.path]: 'cloud' }))} />Keep cloud</label>
              </div>
            </div>
            <div className="mt-1 grid grid-cols-2 text-xs text-zinc-500"><span>Local: {describeEntry(c.local)}</span><span>Cloud: {describeEntry(c.remote)}</span></div>
            {open === c.path && (
              <div className="mt-2">
                {!shown ? <Spinner />
                  : shown.error !== undefined
                    ? (
                      <div className="space-y-2">
                        <p role="alert" className="text-sm text-red-600">Could not load the comparison: {shown.error}</p>
                        <Button onClick={reload}>Reload conflicts</Button>
                      </div>
                    )
                    : shown.detail!.binary || shown.detail!.tooLarge
                      ? <p className="text-sm text-zinc-500">{shown.detail!.binary ? 'Binary file: no text comparison.' : 'Too large to compare.'}</p>
                      : <DiffView left={shown.detail!.localText} right={shown.detail!.remoteText} />}
              </div>
            )}
          </li>
        ))}
      </ul>
      {confirm && (
        <ConfirmDialog title="Some files will be deleted" danger confirmLabel="Resolve" busy={busy} onCancel={() => setConfirm(false)} onConfirm={resolve}
          message={
            <>
              <p className="mb-2">Your choices delete these files. Deletions chosen here are not held back by the delete guard, so check them. The cloud version is saved in a snapshot first, a local file in a local backup.</p>
              <ul className="max-h-48 overflow-auto font-mono text-xs">{deletions.map((d) => <li key={d.path}>{d.path}: deleted {d.where === 'cloud' ? 'in the cloud' : 'on this machine'}</li>)}</ul>
            </>
          } />
      )}
    </div>
  )
}
