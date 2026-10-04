import { vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { ToastProvider, useToast } from './toast'

function Btn() {
  const t = useToast()
  return <button onClick={() => t.show('hello', { details: 'more' })}>go</button>
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

test('a toast auto-dismisses, but not while its details are open', () => {
  render(<ToastProvider><Btn /></ToastProvider>)
  fireEvent.click(screen.getByText('go'))
  fireEvent.click(screen.getByText('Details'))
  act(() => { vi.advanceTimersByTime(30000) })
  expect(screen.getByText('hello')).toBeInTheDocument()
  fireEvent.click(screen.getByLabelText('Dismiss'))
  expect(screen.queryByText('hello')).toBeNull()
})

test('dismissing clears the pending timer and unmount clears the rest', () => {
  const clear = vi.spyOn(globalThis, 'clearTimeout')
  const { unmount } = render(<ToastProvider><Btn /></ToastProvider>)
  fireEvent.click(screen.getByText('go'))
  fireEvent.click(screen.getByLabelText('Dismiss'))
  expect(clear).toHaveBeenCalled()
  fireEvent.click(screen.getByText('go'))
  clear.mockClear()
  unmount()
  expect(clear).toHaveBeenCalled()
})
