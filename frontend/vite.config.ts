/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // Built into the Go package that embeds it; .keep must survive.
  build: { outDir: '../cmd/gui/dist', emptyOutDir: false },
  test: { environment: 'jsdom', setupFiles: ['./src/test/setup.ts'], globals: true },
})
