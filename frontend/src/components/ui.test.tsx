import { vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Modal } from './ui'

test('only the topmost modal handles Escape and modals are aria-modal', () => {
  const a = vi.fn()
  const b = vi.fn()
  render(<><Modal title="A" onClose={a}>x</Modal><Modal title="B" onClose={b}>y</Modal></>)
  expect(screen.getByRole('dialog', { name: 'A' })).toHaveAttribute('aria-modal', 'true')
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(b).toHaveBeenCalledTimes(1)
  expect(a).not.toHaveBeenCalled()
})
