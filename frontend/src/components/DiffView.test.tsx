import { render, screen } from '@testing-library/react'
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
  expect(screen.getByText(/too many lines/i)).toBeInTheDocument()
})
