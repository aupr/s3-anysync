import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import { resetBridge } from './mockBridge'

afterEach(() => {
  cleanup()
  resetBridge()
})
