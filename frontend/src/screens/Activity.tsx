import { useState } from 'react'
import { useToast } from '../components/toast'
import { Badge, Button, EmptyState, Select, td, th } from '../components/ui'
import { api } from '../lib/api'
import { formatTime } from '../lib/format'
import { useStore } from '../lib/store'

const kindTone = { result: 'ok', state: 'neutral', error: 'error' } as const

// Filter values: '' = every entry, OTHER = entries without an action or whose
// action no longer exists, anything else = that action id.
const ALL = ''
const OTHER = '__other__'

export function Activity() {
  const { state } = useStore()
  const toast = useToast()
  const [chosen, setChosen] = useState(ALL)
  const actions = Object.values(state.actions).sort((a, b) => a.action.name.localeCompare(b.action.name))
  // A chosen action that has since been removed falls back to all entries,
  // which is also what the select then shows.
  const filter = chosen === ALL || chosen === OTHER || chosen in state.actions ? chosen : ALL
  const rows = state.activity.filter((a) => {
    if (filter === ALL) return true
    const known = !!a.actionId && a.actionId in state.actions
    return filter === OTHER ? !known : a.actionId === filter
  })
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Activity</h1>
        <div className="flex items-center gap-2">
          <Select aria-label="Filter by action" value={filter} onChange={(e) => setChosen(e.target.value)}>
            <option value={ALL}>All actions</option>
            {actions.map((a) => <option key={a.action.id} value={a.action.id}>{a.action.name}</option>)}
            <option value={OTHER}>Other</option>
          </Select>
          <Button onClick={() => api.openLogFolder().catch(toast.error)}>Open log folder</Button>
        </div>
      </div>
      <p className="text-xs text-zinc-500">Live events since this window opened (the last 500). The full history is in the agent's log files.</p>
      {rows.length === 0 ? <EmptyState title="Nothing yet" /> : (
        <table className="w-full">
          <thead><tr><th className={th}>Time</th><th className={th}>Action</th><th className={th}>Event</th></tr></thead>
          <tbody>
            {rows.map((a) => (
              <tr key={a.id} className={`border-t border-zinc-200 dark:border-zinc-800 ${a.kind === 'error' ? 'bg-red-50 dark:bg-red-950/30' : ''}`}>
                <td className={`${td} whitespace-nowrap`}>{formatTime(a.time)}</td>
                <td className={td}>{a.actionName}</td>
                <td className={td}><Badge tone={kindTone[a.kind]}>{a.kind}</Badge> <span className="break-all">{a.text}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
