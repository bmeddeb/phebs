import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // T5.1: live UI dev against a running `make dev-api` backend
    proxy: { '/api': 'http://localhost:3070' },
  },
})
