import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { RouterProvider, useDirtyGuard, useRouter } from './router'

function Page() {
  const { route, navigate } = useRouter()
  const [dirty, setDirty] = useState(false)
  useDirtyGuard(dirty)
  return (
    <div>
      <span>at:{route.name}</span>
      <button onClick={() => setDirty(true)}>edit</button>
      <button onClick={() => navigate({ name: 'profiles' })}>go</button>
    </div>
  )
}

test('navigation asks before discarding unsaved changes', () => {
  render(<RouterProvider><Page /></RouterProvider>)
  fireEvent.click(screen.getByText('go'))
  expect(screen.getByText('at:profiles')).toBeInTheDocument()
})

test('cancel keeps the page, discard leaves it', () => {
  render(<RouterProvider><Page /></RouterProvider>)
  fireEvent.click(screen.getByText('edit'))
  fireEvent.click(screen.getByText('go'))
  expect(screen.getByText('Discard changes?')).toBeInTheDocument()
  fireEvent.click(screen.getByText('Cancel'))
  expect(screen.getByText('at:actions')).toBeInTheDocument()
  fireEvent.click(screen.getByText('go'))
  fireEvent.click(screen.getByText('Discard'))
  expect(screen.getByText('at:profiles')).toBeInTheDocument()
})
