import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

// Vite config for the DiffMind dashboard. The build emits into ./dist/
// which is embedded into the Go binary via internal/ui/static.go.
//
// Dev mode (`npm run dev`) proxies /api/* to the Go server so the same
// dashboard can be developed against a live DiffMind server.
export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '/healthz': 'http://127.0.0.1:8080',
    },
  },
})
