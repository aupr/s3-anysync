import { allChosen, deletionsFor, describeEntry } from './conflicts'
import type { Conflict } from './types'

const f = (path: string) => ({ path, hash: 'h', size: 1, mtime: 0 })
const cs: Conflict[] = [
  { path: 'a', local: f('a'), remote: f('a'), detectedAt: 't' },
  { path: 'b', local: null, remote: f('b'), detectedAt: 't' },
  { path: 'c', local: f('c'), remote: null, detectedAt: 't' },
]

test('deletionsFor lists files a choice deletes', () => {
  expect(deletionsFor(cs, { a: 'local', b: 'local', c: 'local' })).toEqual([{ path: 'b', where: 'cloud' }])
  expect(deletionsFor(cs, { a: 'cloud', b: 'cloud', c: 'cloud' })).toEqual([{ path: 'c', where: 'local' }])
})

test('allChosen needs a choice for every conflict', () => {
  expect(allChosen(cs, { a: 'local', b: 'cloud' })).toBe(false)
  expect(allChosen(cs, { a: 'local', b: 'cloud', c: 'cloud' })).toBe(true)
})

test('describeEntry reads the mtime as Unix nanoseconds', () => {
  expect(describeEntry(null)).toBe('deleted')
  const ns = Date.UTC(2026, 9, 4, 1, 2, 3) * 1e6
  const s = describeEntry({ path: 'p', hash: 'abcdef0123456789', size: 2048, mtime: ns })
  expect(s).toContain('2.0 KB')
  expect(s).toContain(new Date(Date.UTC(2026, 9, 4, 1, 2, 3)).toLocaleString())
  expect(s).toContain('abcdef01')
  expect(s).not.toContain('abcdef012')
  expect(describeEntry({ path: 'p', hash: 'h', size: 1, mtime: 0 })).not.toContain('1970')
})
