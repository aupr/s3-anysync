import { diffLines } from 'diff'
import { useMemo } from 'react'

// Bounds that keep a big file from freezing the window: more lines than
// MAX_DIFF_LINES on a side are not diffed at all, and at most MAX_DIFF_ROWS
// rows are drawn.
export const MAX_DIFF_LINES = 20000
export const MAX_DIFF_ROWS = 2000

interface Row { l?: string; r?: string; kind: 'same' | 'left' | 'right' }

const countLines = (s: string) => {
  let n = s ? 1 : 0
  for (let i = s.indexOf('\n'); i >= 0 && i < s.length - 1; i = s.indexOf('\n', i + 1)) n++
  return n
}

function buildRows(left: string, right: string): { rows: Row[]; truncated: boolean } {
  const rows: Row[] = []
  for (const part of diffLines(left, right)) {
    for (const line of part.value.replace(/\n$/, '').split('\n')) {
      if (rows.length >= MAX_DIFF_ROWS) return { rows, truncated: true }
      if (part.added) rows.push({ r: line, kind: 'right' })
      else if (part.removed) rows.push({ l: line, kind: 'left' })
      else rows.push({ l: line, r: line, kind: 'same' })
    }
  }
  return { rows, truncated: false }
}

// DiffView shows local (left) and cloud (right) text side by side.
export function DiffView({ left, right }: { left: string; right: string }) {
  const tooMany = countLines(left) > MAX_DIFF_LINES || countLines(right) > MAX_DIFF_LINES
  const { rows, truncated } = useMemo(() => (tooMany ? { rows: [], truncated: false } : buildRows(left, right)), [left, right, tooMany])
  if (tooMany) return <p className="text-sm text-zinc-500">This file has too many lines to compare here. Choose a side based on size and date, or compare the files in an editor.</p>
  const cell = 'whitespace-pre-wrap break-all px-2 font-mono text-xs'
  return (
    <div>
      <div className="grid grid-cols-2 rounded border border-zinc-200 dark:border-zinc-700">
        <div className="border-b border-r border-zinc-200 px-2 py-1 text-xs font-semibold dark:border-zinc-700">This machine</div>
        <div className="border-b border-zinc-200 px-2 py-1 text-xs font-semibold dark:border-zinc-700">Cloud</div>
        {rows.map((row, i) => [
          <div key={`l${i}`} className={`${cell} border-r border-zinc-200 dark:border-zinc-700 ${row.kind === 'left' ? 'bg-red-50 dark:bg-red-900/30' : ''}`}>{row.l ?? ''}</div>,
          <div key={`r${i}`} className={`${cell} ${row.kind === 'right' ? 'bg-green-50 dark:bg-green-900/30' : ''}`}>{row.r ?? ''}</div>,
        ])}
      </div>
      {truncated && <p className="mt-1 text-xs text-zinc-500">Diff truncated after {MAX_DIFF_ROWS} rows.</p>}
    </div>
  )
}
