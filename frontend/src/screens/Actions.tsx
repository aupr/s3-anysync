import { useEffect, useRef, useState } from 'react'
import { Badge, Button, EmptyState, Spinner, td, th } from '../components/ui'
import { formatTime, isZeroTime, stateLabels, stateTone } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import { useActionCommands } from '../lib/useActionCommands'
import { JoinWizard } from './JoinWizard'
import { NewActionWizard } from './NewActionWizard'

export function Actions() {
  const { state } = useStore()
  const { navigate } = useRouter()
  const cmd = useActionCommands()
  const [wizard, setWizard] = useState<'new' | 'join' | null>(null)
  // Actions with a command request in flight; their buttons are disabled so a
  // second click cannot start a second request.
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set())
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const run = async (id: string, fn: (id: string) => Promise<unknown>) => {
    if (pending.has(id)) return
    setPending((s) => new Set(s).add(id))
    try {
      await fn(id)
    } finally {
      if (alive.current) setPending((s) => { const n = new Set(s); n.delete(id); return n })
    }
  }
  const list = Object.values(state.actions).sort((a, b) => a.action.name.localeCompare(b.action.name))
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Actions</h1>
        <div className="space-x-2">
          <Button onClick={() => setWizard('join')}>Join existing</Button>
          <Button variant="primary" onClick={() => setWizard('new')}>New action</Button>
        </div>
      </div>
      {list.length === 0 ? (
        <EmptyState title="No actions yet">Create an action for the settings you want to sync, or join one created on another machine.</EmptyState>
      ) : (
        <table className="w-full">
          <thead><tr><th className={th}>Action</th><th className={th}>State</th><th className={th}>Last sync</th><th className={th}>Next run</th><th className={th} /></tr></thead>
          <tbody>
            {list.map((a) => {
              const id = a.action.id
              const name = a.action.name
              const paused = a.state === 'paused'
              const wait = pending.has(id)
              return (
                <tr key={id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className={td}>
                    <button type="button" className="font-medium hover:underline" onClick={() => navigate({ name: 'action', id, tab: 'overview' })}>{name}</button>
                    {a.action.encrypt && <span className="ml-2 text-xs text-zinc-500">encrypted</span>}
                  </td>
                  <td className={`${td} space-y-1`}>
                    <div className="flex items-center gap-2"><Badge tone={stateTone(a.state)}>{stateLabels[a.state]}</Badge>{a.running && <Spinner label="Running" />}</div>
                    {a.conflicts > 0 && <button type="button" className="block text-xs text-amber-700 underline" onClick={() => navigate({ name: 'conflicts', id })}>{a.conflicts} conflicts</button>}
                    {a.pendingDeletes > 0 && <button type="button" className="block text-xs text-amber-700 underline" onClick={() => navigate({ name: 'deletes', id })}>{a.pendingDeletes} deletions to review</button>}
                  </td>
                  <td className={td}>
                    {formatTime(a.lastRun)}
                    {a.lastOutcome && <div className="text-xs text-zinc-500">{a.lastOutcome}{a.lastSummary ? `: ${a.lastSummary}` : ''}</div>}
                    {a.lastError && <div className="text-xs text-red-600">{a.lastError}</div>}
                  </td>
                  <td className={td}>{isZeroTime(a.nextRun) ? '—' : formatTime(a.nextRun)}</td>
                  <td className={`${td} space-x-2 whitespace-nowrap text-right`}>
                    {a.running
                      ? <Button aria-label={`Cancel ${name}`} disabled={wait} onClick={() => run(id, cmd.cancel)}>Cancel</Button>
                      : <Button aria-label={`Sync now ${name}`} onClick={() => run(id, cmd.sync)} disabled={paused || wait}>Sync now</Button>}
                    {paused
                      ? <Button aria-label={`Resume ${name}`} disabled={wait} onClick={() => run(id, cmd.resume)}>Resume</Button>
                      : <Button aria-label={`Pause ${name}`} disabled={wait} onClick={() => run(id, cmd.pause)}>Pause</Button>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {wizard === 'new' && <NewActionWizard onClose={() => setWizard(null)} />}
      {wizard === 'join' && <JoinWizard onClose={() => setWizard(null)} />}
    </div>
  )
}
