import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/server/static',
    emptyOutDir: true,
  },
  server: {
    host: '0.0.0.0',
    port: 8228,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
