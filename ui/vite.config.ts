import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // T16.5: canonical fixture imports are test-only; keep the dev-server
    // filesystem exception pinned to that public demo corpus.
    fs: { allow: ['../docs/fixtures/investigations'] },
    // T5.1: live UI dev against a running `make dev-api` backend
    proxy: { '/api': 'http://localhost:3070' },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test-setup.ts'],
  },
})
