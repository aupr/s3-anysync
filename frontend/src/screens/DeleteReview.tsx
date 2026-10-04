import { useEffect, useRef, useState } from 'react'
import { useToast } from '../components/toast'
import { Button, ConfirmDialog, EmptyState, Spinner } from '../components/ui'
import { api, toAgentError } from '../lib/api'
import { describeError } from '../lib/errors'
import { formatTime, summarizeResult } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import { useLoad } from '../lib/useLoad'

// How many paths are drawn; the approval still covers all of them.
const LIST_CAP = 2000

export function DeleteReview({ id }: { id: string }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  // Boxed, because a null answer (nothing pending) is a valid result.
  const { data, error, reload } = useLoad(() => api.pendingDeletes(id).then((p) => ({ p })), (e) => describeError(e).message, [id])
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [showAll, setShowAll] = useState(false)
  const busyRef = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const name = state.actions[id]?.action.name ?? id
  const back = () => navigate({ name: 'action', id, tab: 'overview' })

  if (data === null) {
    return error
      ? <div className="space-y-2"><p role="alert" className="text-sm text-red-600">Could not load the deletions: {error}</p><Button onClick={reload}>Retry</Button></div>
      : <Spinner />
  }
  const pending = data.p
  const paths = [...(pending?.paths ?? [])].sort()
  if (!pending || paths.length === 0 || pending.approved) {
    return <EmptyState title="Nothing to review"><Button onClick={back}>Back to {name}</Button></EmptyState>
  }
  const approve = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      // Exactly the list on screen: the agent refuses a different saved list.
      const r = await api.approveDeletes(id, paths)
      if (r.outcome === 'deletes-pending') {
        toast.show('The list of deletions changed (or grew), so nothing was approved. Review the new list.')
        reload()
      } else if (r.outcome === 'conflict') {
        toast.show('The sync found conflicts, so nothing was deleted. Resolve them first.')
        if (alive.current) navigate({ name: 'conflicts', id })
      } else if (r.deferred?.length) {
        toast.show(`Approved; ${r.deferred.length} file(s) were not deleted because they changed meanwhile (listed in the history)`)
        if (alive.current) back()
      } else {
        toast.show(`Done: ${summarizeResult(r)}`)
        if (alive.current) back()
      }
    } catch (e) {
      if (toAgentError(e).code === 'invalid') {
        toast.show('No deletions are waiting for approval any more.', { details: toAgentError(e).message })
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
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Deletions held back in {name}</h1>
      <p className="text-sm text-zinc-500">
        A sync on {formatTime(pending.detectedAt)} would delete many files at once, so nothing was applied. Approve only if these deletions are intended;
        the copy that is deleted is saved first (cloud files in a snapshot, local files in a local backup).
      </p>
      {error && <p role="alert" className="text-sm text-red-600">Could not refresh the list: {error}</p>}
      <ul className="max-h-[60vh] overflow-auto rounded border border-zinc-200 p-2 font-mono text-xs dark:border-zinc-800">
        {(showAll ? paths : paths.slice(0, LIST_CAP)).map((p) => <li key={p}>{p}</li>)}
        {!showAll && paths.length > LIST_CAP && <li className="text-zinc-500">…and {paths.length - LIST_CAP} more (all are approved together)</li>}
      </ul>
      {!showAll && paths.length > LIST_CAP && <Button onClick={() => setShowAll(true)}>{`Show all ${paths.length} paths`}</Button>}
      <Button variant="danger" disabled={busy} onClick={() => setConfirm(true)}>{`Approve ${paths.length} deletion(s)`}</Button>
      {confirm && (
        <ConfirmDialog title="Approve the deletions?" danger confirmLabel="Approve" busy={busy} onCancel={() => setConfirm(false)} onConfirm={approve}
          message={`${paths.length} file(s) are deleted on the side where they still exist.`} />
      )}
    </div>
  )
}
