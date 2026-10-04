import { vi } from 'vitest'
vi.mock('diff', () => ({ diffLines: vi.fn(() => undefined) }))
import { render, screen } from '@testing-library/react'
import { diffLines } from 'diff'
import { DiffView, DIFF_TIMEOUT_MS } from './DiffView'

test('a diff that times out (jsdiff returns undefined) shows the fallback note', () => {
  render(<DiffView left={'a\n'} right={'b\n'} />)
  expect(screen.getByText(/too different to compare here/)).toBeInTheDocument()
  expect(vi.mocked(diffLines).mock.calls[0][2]).toMatchObject({ timeout: DIFF_TIMEOUT_MS, stripTrailingCr: true })
})
