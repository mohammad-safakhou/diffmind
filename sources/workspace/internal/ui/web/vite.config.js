import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

// Build into dist/ which the Go binary embeds. During dev, the API runs on
// :8090; proxy /api and /healthz there so `npm run dev` works against a live
// backend.
export default defineConfig({
  plugins: [preact()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8090',
      '/healthz': 'http://127.0.0.1:8090',
    },
  },
})
