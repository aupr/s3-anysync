import { Badge, Button, EmptyState } from '../components/ui'
import { stateLabels, stateTone } from '../lib/format'
import { useRouter, type Tab } from '../lib/router'
import { useStore } from '../lib/store'
import { HistoryTab } from './tabs/History'
import { ItemsTab } from './tabs/Items'
import { Overview } from './tabs/Overview'
import { RulesTab } from './tabs/Rules'
import { SnapshotsTab } from './tabs/Snapshots'

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'rules', label: 'Rules' },
  { id: 'items', label: 'Items' },
  { id: 'snapshots', label: 'Snapshots' },
  { id: 'history', label: 'History' },
]

export function ActionDetail({ id, tab }: { id: string; tab: Tab }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const st = state.actions[id]
  if (!st) {
    return (
      <EmptyState title="This action no longer exists on this machine.">
        <Button onClick={() => navigate({ name: 'actions' })}>Back to actions</Button>
      </EmptyState>
    )
  }
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <button type="button" className="text-sm text-zinc-500 hover:underline" onClick={() => navigate({ name: 'actions' })}>Actions /</button>
        <h1 className="text-xl font-semibold">{st.action.name}</h1>
        <Badge tone={st.running ? 'neutral' : stateTone(st.state)}>{st.running ? 'Running' : stateLabels[st.state]}</Badge>
      </div>
      <div className="flex gap-1 border-b border-zinc-200 dark:border-zinc-800">
        {tabs.map((t) => (
          <button key={t.id} type="button" aria-current={t.id === tab ? 'page' : undefined} onClick={() => navigate({ name: 'action', id, tab: t.id })}
            className={`px-3 py-2 text-sm ${t.id === tab ? 'border-b-2 border-blue-600 font-medium' : 'text-zinc-500'}`}>{t.label}</button>
        ))}
      </div>
      {tab === 'overview' && <Overview key={id} st={st} />}
      {tab === 'rules' && <RulesTab key={id} st={st} />}
      {tab === 'items' && <ItemsTab key={id} st={st} />}
      {tab === 'snapshots' && <SnapshotsTab key={id} st={st} />}
      {tab === 'history' && <HistoryTab key={id} st={st} />}
    </div>
  )
}
