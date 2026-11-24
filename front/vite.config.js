import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server talks to the Go server next door: `go run ./cmd/web` first.
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist' },
  server: {
    proxy: {
      '/api': {
        target: process.env.API_TARGET || 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
})
