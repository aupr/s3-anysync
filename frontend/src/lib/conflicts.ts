import { formatSize } from './format'
import type { Conflict, FileEntry, Side } from './types'

// deletionsFor lists the deletions a set of choices causes (internal/engine
// sync.go applyOverrides): keeping the side where the file is gone deletes it
// on the other side. where is the side that LOSES the file. Deletions chosen
// here are not held back by the delete guard, so the GUI confirms them.
export function deletionsFor(conflicts: Conflict[], choices: Record<string, Side>): { path: string; where: 'local' | 'cloud' }[] {
  const out: { path: string; where: 'local' | 'cloud' }[] = []
  for (const c of conflicts) {
    const side = choices[c.path]
    if (side === 'local' && !c.local) out.push({ path: c.path, where: 'cloud' })
    if (side === 'cloud' && !c.remote) out.push({ path: c.path, where: 'local' })
  }
  return out
}

export const allChosen = (conflicts: Conflict[], choices: Record<string, Side>) => conflicts.every((c) => !!choices[c.path])

// describeEntry summarizes one side of a conflict. FileEntry.mtime is Unix
// nanoseconds (model.FileEntry.MTime); null means the file is deleted there.
export function describeEntry(e: FileEntry | null): string {
  if (!e) return 'deleted'
  const parts = [formatSize(e.size)]
  if (e.mtime > 0) parts.push(new Date(e.mtime / 1e6).toLocaleString())
  parts.push(e.hash.slice(0, 8))
  return parts.join(' · ')
}
