import { diffLines, type Change, type LinesOptions } from 'diff'
import { useMemo, useState } from 'react'

// Bounds that keep a big file from freezing the window: more lines than
// MAX_DIFF_LINES on a side are not diffed at all (jsdiff is O(N*D) and runs in
// render), a diff running longer than DIFF_TIMEOUT_MS is abandoned, and at most
// MAX_DIFF_ROWS rows are drawn.
export const MAX_DIFF_LINES = 3000
export const MAX_DIFF_ROWS = 2000
export const DIFF_TIMEOUT_MS = 1500
const RAW_LINES = 2000

interface Row { l?: string; r?: string; kind: 'same' | 'left' | 'right' }

const countLines = (s: string) => {
  let n = s ? 1 : 0
  for (let i = s.indexOf('\n'); i >= 0 && i < s.length - 1; i = s.indexOf('\n', i + 1)) n++
  return n
}

// timeout and stripTrailingCr exist in the installed diff@5 but not in its types.
const diffOptions = { timeout: DIFF_TIMEOUT_MS, stripTrailingCr: true } as unknown as LinesOptions

type Built = { skipped: true } | { skipped: false; rows: Row[]; truncated: boolean; eolOnly: boolean }

function build(left: string, right: string): Built {
  if (countLines(left) > MAX_DIFF_LINES || countLines(right) > MAX_DIFF_LINES) return { skipped: true }
  const parts = diffLines(left, right, diffOptions) as Change[] | undefined
  if (!parts) return { skipped: true }
  const rows: Row[] = []
  let truncated = false
  let changed = false
  outer: for (const part of parts) {
    if (part.added || part.removed) changed = true
    for (const line of part.value.replace(/\n$/, '').split('\n')) {
      if (rows.length >= MAX_DIFF_ROWS) { truncated = true; break outer }
      if (part.added) rows.push({ r: line, kind: 'right' })
      else if (part.removed) rows.push({ l: line, kind: 'left' })
      else rows.push({ l: line, r: line, kind: 'same' })
    }
  }
  return { skipped: false, rows, truncated, eolOnly: !changed && left !== right }
}

const firstLines = (s: string) => s.split('\n').slice(0, RAW_LINES).join('\n')

// DiffView shows local (left) and cloud (right) text side by side.
export function DiffView({ left, right }: { left: string; right: string }) {
  const built = useMemo(() => build(left, right), [left, right])
  const [raw, setRaw] = useState(false)
  if (built.skipped) {
    return (
      <div className="space-y-2">
        <p className="text-sm text-zinc-500">The files are too different to compare here; compare them in an editor.</p>
        {raw
          ? (
            <div className="grid grid-cols-2 gap-2">
              <pre aria-label="This machine text" className="max-h-96 overflow-auto rounded border border-zinc-200 p-2 text-xs dark:border-zinc-700">{firstLines(left)}</pre>
              <pre aria-label="Cloud text" className="max-h-96 overflow-auto rounded border border-zinc-200 p-2 text-xs dark:border-zinc-700">{firstLines(right)}</pre>
            </div>
          )
          : <button type="button" className="text-sm underline" onClick={() => setRaw(true)}>Show each side's text</button>}
        {raw && <p className="text-xs text-zinc-500">Only the first {RAW_LINES} lines of each side are shown.</p>}
      </div>
    )
  }
  const { rows, truncated, eolOnly } = built
  const cell = 'whitespace-pre-wrap break-all px-2 font-mono text-xs'
  return (
    <div>
      {eolOnly && <p className="mb-1 text-sm text-zinc-500">These files differ only in line endings.</p>}
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
