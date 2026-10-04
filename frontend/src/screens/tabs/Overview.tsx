import { useEffect, useRef, useState } from 'react'
import { useToast } from '../../components/toast'
import { Button, ConfirmDialog, Spinner } from '../../components/ui'
import { api, toAgentError } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { directionLabels, formatTime, isZeroTime } from '../../lib/format'
import { useRouter } from '../../lib/router'
import { useStore } from '../../lib/store'
import type { ActionStatus } from '../../lib/types'
import { useActionCommands } from '../../lib/useActionCommands'

// Failures of LeaveAction (internal/app/manage.go) that "leave on this machine
// only" cannot help with: they are transient (another process or machine holds
// a lock, the agent is going away) and succeed on a retry. Every other failure
// means the shared copy in the bucket cannot be updated (access denied, no such
// bucket, the action or profile is gone, missing or wrong passphrase, network),
// which is what the force option is for.
const retryLater = new Set(['busy', 'locked', 'lock-lost', 'unreachable', 'unauthorized', 'unavailable', 'cancelled'])

export function Overview({ st }: { st: ActionStatus }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const cmd = useActionCommands()
  const toast = useToast()
  const [leaving, setLeaving] = useState<'ask' | 'force' | null>(null)
  const [forceReason, setForceReason] = useState('')
  const [leaveBusy, setLeaveBusy] = useState(false)
  const leaveRef = useRef(false)
  // Commands with a request in flight; their button is disabled so a second
  // click cannot start a second request.
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set())
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const id = st.action.id
  const machineId = state.status?.machineId ?? ''
  const dir = st.action.machineDirections?.[machineId] ?? st.action.direction

  const run = async (key: string, fn: (id: string) => Promise<unknown>) => {
    if (pending.has(key)) return
    setPending((s) => new Set(s).add(key))
    try {
      await fn(id)
    } finally {
      if (alive.current) setPending((s) => { const n = new Set(s); n.delete(key); return n })
    }
  }

  const leave = async (force: boolean) => {
    if (leaveRef.current) return
    leaveRef.current = true
    setLeaveBusy(true)
    try {
      await api.leaveAction(id, force)
      if (!alive.current) return
      setLeaving(null)
      navigate({ name: 'actions' })
    } catch (e) {
      if (!alive.current) return
      const code = toAgentError(e).code
      if (force && code === 'not-found') {
        // The forced retry found nothing left locally: the action is gone.
        setLeaving(null)
        navigate({ name: 'actions' })
      } else if (!force && !retryLater.has(code)) {
        const d = describeError(e)
        setForceReason(d.details ? `${d.message} (${d.details})` : d.message)
        setLeaving('force')
      } else {
        setLeaving(null)
        toast.error(e)
      }
    } finally {
      leaveRef.current = false
      if (alive.current) setLeaveBusy(false)
    }
  }
  const cancelLeave = () => { if (!leaveRef.current) setLeaving(null) }

  return (
    <div className="space-y-4">
      {st.state === 'paused-conflict' && (
        <div className="flex items-center justify-between rounded-md bg-amber-100 p-3 text-sm text-amber-900 dark:bg-amber-900/40 dark:text-amber-100">
          <span>{st.conflicts} file(s) changed on both sides. The action is paused until you choose.</span>
          <Button onClick={() => navigate({ name: 'conflicts', id })}>Resolve conflicts</Button>
        </div>
      )}
      {st.state === 'paused-deletes' && (
        <div className="flex items-center justify-between rounded-md bg-amber-100 p-3 text-sm text-amber-900 dark:bg-amber-900/40 dark:text-amber-100">
          <span>{st.pendingDeletes} deletion(s) are held back by the delete guard.</span>
          <Button onClick={() => navigate({ name: 'deletes', id })}>Review deletions</Button>
        </div>
      )}
      {st.state === 'error' && (
        <div className="flex items-center justify-between rounded-md bg-red-100 p-3 text-sm text-red-900 dark:bg-red-900/40 dark:text-red-100">
          <span>{st.lastError} ({st.failures} failure(s){isZeroTime(st.nextRun) ? '' : `, next retry ${formatTime(st.nextRun)}`})</span>
          <Button disabled={pending.has('resume')} onClick={() => run('resume', cmd.resume)}>Retry now</Button>
        </div>
      )}
      <dl className="grid grid-cols-[12rem_1fr] gap-y-2 text-sm">
        <dt className="text-zinc-500">Direction on this machine</dt><dd>{directionLabels[dir]}</dd>
        <dt className="text-zinc-500">Last sync</dt><dd>{formatTime(st.lastRun)} {st.lastOutcome && `(${st.lastOutcome})`}</dd>
        <dt className="text-zinc-500">Summary</dt><dd>{st.lastSummary || '—'}</dd>
        <dt className="text-zinc-500">Next run</dt><dd>{isZeroTime(st.nextRun) ? '—' : formatTime(st.nextRun)}</dd>
        <dt className="text-zinc-500">Machines</dt><dd>{Object.values(st.action.machines ?? {}).join(', ')}</dd>
        <dt className="text-zinc-500">Encryption</dt><dd>{st.action.encrypt ? 'On' : 'Off'}</dd>
      </dl>
      <div className="flex flex-wrap items-center gap-2">
        {st.running
          ? <><Spinner label="Running" /><Button disabled={pending.has('cancel')} onClick={() => run('cancel', cmd.cancel)}>Cancel</Button></>
          : <Button variant="primary" disabled={st.state === 'paused' || pending.has('sync')} onClick={() => run('sync', cmd.sync)}>Sync now</Button>}
        <Button disabled={st.running || pending.has('snapshot')} onClick={() => run('snapshot', cmd.snapshot)}>Snapshot now</Button>
        {st.state === 'paused'
          ? <Button disabled={pending.has('resume')} onClick={() => run('resume', cmd.resume)}>Resume</Button>
          : <Button disabled={pending.has('pause')} onClick={() => run('pause', cmd.pause)}>Pause</Button>}
        <Button variant="danger" onClick={() => setLeaving('ask')}>Leave action</Button>
      </div>
      {leaving === 'ask' && (
        <ConfirmDialog title={`Leave ${st.action.name}?`} danger confirmLabel="Leave" busy={leaveBusy}
          message="This machine stops syncing the action. Local files and the cloud copy are kept; other machines keep syncing."
          onCancel={cancelLeave} onConfirm={() => leave(false)} />
      )}
      {leaving === 'force' && (
        <ConfirmDialog title="Leave anyway?" danger confirmLabel="Leave on this machine only" busy={leaveBusy}
          message={<>The bucket could not be updated: {forceReason}. Leave on this machine only? Other machines will still list this machine until the bucket is reachable.</>}
          onCancel={cancelLeave} onConfirm={() => leave(true)} />
      )}
    </div>
  )
}
