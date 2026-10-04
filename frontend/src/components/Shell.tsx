import type { ReactNode } from 'react'
import { useRouter, type Route } from '../lib/router'
import { useStore } from '../lib/store'
import { useTheme, type Theme } from '../lib/theme'

const nav: { label: string; route: Route; match: Route['name'][] }[] = [
  { label: 'Actions', route: { name: 'actions' }, match: ['actions', 'action', 'conflicts', 'deletes'] },
  { label: 'Profiles', route: { name: 'profiles' }, match: ['profiles'] },
  { label: 'Activity', route: { name: 'activity' }, match: ['activity'] },
  { label: 'Settings', route: { name: 'settings' }, match: ['settings'] },
]

export function Shell({ children }: { children: ReactNode }) {
  const { state } = useStore()
  const { route, navigate } = useRouter()
  const [theme, setTheme] = useTheme()
  const conn = state.connection
  return (
    <div className="flex h-full">
      <nav className="w-48 shrink-0 border-r border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-950">
        <div className="mb-4 px-2 text-lg font-semibold">s3-anysync</div>
        {nav.map((n) => (
          <button key={n.label} type="button" onClick={() => navigate(n.route)}
            className={`block w-full rounded-md px-2 py-1.5 text-left text-sm ${n.match.includes(route.name) ? 'bg-blue-100 font-medium text-blue-900 dark:bg-blue-900/40 dark:text-blue-100' : 'hover:bg-zinc-100 dark:hover:bg-zinc-800'}`}>
            {n.label}
          </button>
        ))}
      </nav>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-zinc-200 px-4 py-2 text-sm dark:border-zinc-800">
          <span>Machine: <strong>{state.status?.machineName ?? '…'}</strong></span>
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1">
              <span className={`h-2 w-2 rounded-full ${conn.connected ? 'bg-green-500' : 'bg-amber-500'}`} />
              {conn.connected ? 'Agent connected' : 'Reconnecting…'}
            </span>
            <select aria-label="Theme" value={theme} onChange={(e) => setTheme(e.target.value as Theme)}
              className="rounded border border-zinc-300 bg-transparent px-1 py-0.5 dark:border-zinc-700">
              <option value="system">System</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
          </div>
        </header>
        {!conn.connected && (
          <div role="alert" className="bg-amber-100 px-4 py-2 text-sm text-amber-900 dark:bg-amber-900/40 dark:text-amber-100">
            The agent is not reachable{conn.reason ? ` (${conn.reason})` : ''}. Reconnecting every 2 seconds; changes cannot be saved until it is back.
          </div>
        )}
        <main className="min-h-0 flex-1 overflow-auto p-4">{children}</main>
      </div>
    </div>
  )
}
