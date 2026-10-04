import { Button, EmptyState, Spinner, td, th } from '../../components/ui'
import { api } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { formatTime } from '../../lib/format'
import type { ActionStatus } from '../../lib/types'
import { useLoad } from '../../lib/useLoad'

const LIMIT = 100

const kindLabels: Record<string, string> = {
  sync: 'Sync',
  resolve: 'Resolve',
  snapshot: 'Snapshot',
  restore: 'Restore',
  'approve-deletes': 'Approve deletions',
}

const outcomeLabels: Record<string, string> = {
  ok: 'OK',
  conflict: 'Conflicts',
  error: 'Error',
  'deletes-pending': 'Deletions waiting',
  cancelled: 'Cancelled',
}

export function HistoryTab({ st }: { st: ActionStatus }) {
  const id = st.action.id
  const { data: rows, error, reload } = useLoad(() => api.history(id, LIMIT), (e) => {
    const d = describeError(e)
    return d.details ? `${d.message} (${d.details})` : d.message
  }, [id, st.lastRun])
  const problem = error && (
    <div role="alert" className="flex items-center gap-3 text-sm text-red-600">
      <span>Could not load the history: {error}</span>
      <Button onClick={reload}>Retry</Button>
    </div>
  )
  if (rows === null) return problem || <Spinner />
  if (rows.length === 0) return <>{problem}<EmptyState title="Nothing has run yet" /></>
  return (
    <div className="space-y-2">
      {problem}
      <table className="w-full">
        <thead><tr><th className={th}>Started</th><th className={th}>Kind</th><th className={th}>Outcome</th><th className={th}>Details</th></tr></thead>
        <tbody>
          {rows.map((h) => (
            <tr key={h.id} className="border-t border-zinc-200 dark:border-zinc-800">
              <td className={td}>{formatTime(h.startedAt)}</td>
              <td className={td}>{kindLabels[h.kind] ?? h.kind}</td>
              <td className={td}>{outcomeLabels[h.outcome] ?? h.outcome}</td>
              <td className={`${td} break-all text-xs`}>{h.message}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length >= LIMIT && <p className="text-xs text-zinc-500">Showing the newest {LIMIT} runs.</p>}
    </div>
  )
}
