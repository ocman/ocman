import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Extra hostnames (Tailscale, LAN, reverse proxies, etc.) that Vite's dev
// and preview servers are allowed to respond to. Comma-separated list,
// e.g. OCMAN_ALLOWED_HOSTS=foo.tailnet.ts.net,bar.lan
//
// Vite only blocks non-matching Host headers on the server/preview endpoints;
// production builds are unaffected, so this is purely a local dev-ergonomics knob.
const extraAllowedHosts = (process.env.OCMAN_ALLOWED_HOSTS ?? '')
  .split(',')
  .map((h) => h.trim())
  .filter(Boolean)

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/server/static',
    emptyOutDir: true,
  },
  server: {
    host: '0.0.0.0',
    port: 8228,
    allowedHosts: extraAllowedHosts,
    proxy: {
      '/api': 'http://localhost:8229',
    },
  },
  preview: {
    host: '0.0.0.0',
    port: 8228,
    allowedHosts: extraAllowedHosts,
    proxy: {
      '/api': 'http://localhost:8229',
    },
  },
})
