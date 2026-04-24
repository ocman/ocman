import { defineConfig, type Plugin } from 'vite'
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

// When STRIP_TESTIDS=1, strip data-testid (and related test-only attrs) from
// JSX source at build time so they don't ship to end users. E2E tests run
// against `vite preview` without this flag so selectors keep working.
//
// The production `make build-frontend` target sets STRIP_TESTIDS=1; local
// dev, the e2e preview build, and any plain `vite build` keep the attributes.
//
// Implementation note: `@vitejs/plugin-react` v6 switched from Babel to Oxc
// and no longer exposes a `babel.plugins` hook, so we can't use
// babel-plugin-jsx-remove-data-test-id. Instead this regex-based transform
// strips test-only attributes before the compiler sees them.
// Intentional limitations:
//   - Only literal JSX attributes (data-testid="x" or data-testid={"x"}) are
//     matched. Attributes added via spread props or computed keys are NOT
//     stripped here; scripts/check-no-testids.sh catches any residue.
//   - Template strings / comments containing "data-testid=" would be rewritten
//     too, which is acceptable for this codebase (no such strings exist) and
//     easy to audit.
const stripTestIds = process.env.STRIP_TESTIDS === '1'

const TEST_ATTR_RE =
  /\s(?:data-testid|data-test-id|data-test)\s*=\s*(?:"[^"]*"|'[^']*'|\{[^}]*\})/g

function stripTestIdsPlugin(): Plugin {
  return {
    name: 'ocman:strip-test-ids',
    enforce: 'pre',
    transform(code, id) {
      if (!/\.(tsx|jsx)$/.test(id)) return null
      if (!TEST_ATTR_RE.test(code)) return null
      // RegExp with /g retains lastIndex across .test() calls; reset it.
      TEST_ATTR_RE.lastIndex = 0
      return { code: code.replace(TEST_ATTR_RE, ''), map: null }
    },
  }
}

export default defineConfig({
  plugins: [
    ...(stripTestIds ? [stripTestIdsPlugin()] : []),
    react(),
  ],
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
