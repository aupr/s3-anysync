import { fireEvent, render, screen } from '@testing-library/react'
import { DiffView, MAX_DIFF_LINES, MAX_DIFF_ROWS } from './DiffView'

test('shows removed and added lines on their side', () => {
  render(<DiffView left={'same\nold\n'} right={'same\nnew\n'} />)
  expect(screen.getAllByText('same')).toHaveLength(2)
  expect(screen.getByText('old')).toBeInTheDocument()
  expect(screen.getByText('new')).toBeInTheDocument()
})

test('caps the rows of a long diff', () => {
  const left = Array.from({ length: MAX_DIFF_ROWS + 500 }, (_, i) => `l${i}`).join('\n')
  render(<DiffView left={left} right="" />)
  expect(screen.getByText(/Diff truncated/)).toBeInTheDocument()
  expect(screen.queryByText(`l${MAX_DIFF_ROWS + 10}`)).not.toBeInTheDocument()
})

test('does not even diff files with too many lines', () => {
  const big = 'x\n'.repeat(MAX_DIFF_LINES + 1)
  render(<DiffView left={big} right="y" />)
  expect(screen.getByText(/too different to compare here/)).toBeInTheDocument()
})

test('a big, fully different pair falls back quickly and offers the raw text', () => {
  const a = Array.from({ length: 4000 }, (_, i) => `a${i}`).join('\n')
  const b = Array.from({ length: 4000 }, (_, i) => `b${i}`).join('\n')
  const t0 = Date.now()
  render(<DiffView left={a} right={b} />)
  expect(Date.now() - t0).toBeLessThan(1500)
  expect(screen.getByText('The files are too different to compare here; compare them in an editor.')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /Show each side/ }))
  expect(screen.getByLabelText('This machine text').textContent).toContain('a1999')
  expect(screen.getByLabelText('This machine text').textContent).not.toContain('a2000')
})

test('files differing only in line endings are not shown as changed', () => {
  render(<DiffView left={'a\r\nb\r\n'} right={'a\nb\n'} />)
  expect(screen.getByText('These files differ only in line endings.')).toBeInTheDocument()
  expect(screen.getAllByText('a')).toHaveLength(2)
})
