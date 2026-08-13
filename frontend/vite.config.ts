import { defineConfig, type Plugin, type ProxyOptions } from 'vite'
import react, { reactCompilerPreset } from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'

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
//     stripped here.
//   - Template strings / comments containing "data-testid=" would be rewritten
//     too, which is acceptable for this codebase (no such strings exist) and
//     easy to audit.
const stripTestIds = process.env.STRIP_TESTIDS === '1'

const TEST_ATTR_RE =
  /\s(?:data-testid|data-test-id|data-test)\s*=\s*(?:"[^"]*"|'[^']*'|\{[^}]*\})/g

// Shared proxy `configure` for /api and /mcp. The `error` handler is the
// important part: when the Go backend on :8229 is down (e.g. the e2e
// `vite preview` server runs with no backend, and unmocked /api/events SSE
// or /api/term/ws upgrade requests slip through Playwright's route mocks),
// http-proxy emits an unhandled `error`. Without a listener that ECONNREFUSED
// bubbles up as an uncaught exception and kills the whole Vite server mid-run,
// which makes every subsequent e2e test fail with ERR_CONNECTION_REFUSED.
// Swallowing it keeps the static server alive.
// ponytail: minimal — just keep the server alive on upstream failure.
const configureApiProxy: ProxyOptions['configure'] = (proxy) => {
  proxy.on('error', (_err, _req, target) => {
    // `target` is the client response (HTTP) or socket (WS upgrade).
    // End it cleanly so the browser sees a failed request instead of a
    // hung connection — and, crucially, so the unhandled `error` event
    // doesn't crash the whole Vite server.
    const res = target as { writableEnded?: boolean; end?: () => void; destroy?: () => void }
    try {
      if (res && typeof res.end === 'function' && !res.writableEnded) res.end()
      else if (res && typeof res.destroy === 'function') res.destroy()
    } catch { /* socket already closed */ }
  })
  // During Playwright e2e the preview server proxies to a backend that
  // isn't there (or to mocked routes); draining proxied response bodies
  // keeps sockets from hanging and the vite server stable across the
  // suite. This does NOT break real SSE — the proxy still pipes chunks to
  // the client; this listener only also reads them. The live-update fix
  // was server-side (statusRecorder implementing http.Flusher).
  proxy.on('proxyRes', (proxyRes) => {
    proxyRes.on('data', () => {})
  })
}

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

// When building for the Wails desktop bundle, output into the standard
// Wails dist dir so `wails build` picks it up.  The normal `make build`
// still targets ../internal/webui/static (the go:embed source).
const isWailsBuild = process.env.WAILS_BUILD === '1'
const buildOutDir = isWailsBuild ? '../frontend/dist' : '../internal/webui/static'

export default defineConfig({
  plugins: [
    ...(stripTestIds ? [stripTestIdsPlugin()] : []),
    react(),
    // React Compiler — auto-memoizes components and hooks at build
    // time so we don't have to remember useMemo/useCallback for every
    // prop/store-adapter object. Catches the class of "unstable
    // reference passed to a hook running an unconditional effect"
    // bugs that have plagued this codebase (see commits a1d1140 etc).
    babel({ presets: [reactCompilerPreset()] }),
  ],
  build: {
    outDir: buildOutDir,
    emptyOutDir: true,
  },
  server: {
    host: '0.0.0.0',
    port: 8228,
    allowedHosts: extraAllowedHosts,
     proxy: {
       // Disable response buffering for /api so SSE event streams
       // (e.g. /api/session/{id}/events) are forwarded to the client
       // immediately rather than held until the stream closes.
       //
       // `ws: true` is required so WebSocket upgrade requests (the live
       // terminal at /api/term/ws) are proxied to the Go backend.
       // Without it Vite silently drops the upgrade and the socket hangs
       // in "Connecting…".
       '/api': {
         target: 'http://localhost:8229',
         ws: true,
         configure: configureApiProxy,
       },
        // MCP server — Streamable HTTP transport (POST + GET/SSE).
        '/mcp': {
          target: 'http://localhost:8229',
          configure: configureApiProxy,
        },
     },
   },
   preview: {
     host: '0.0.0.0',
     port: 8228,
     allowedHosts: extraAllowedHosts,
      proxy: {
        '/api': {
          target: 'http://localhost:8229',
          ws: true,
          configure: configureApiProxy,
        },
        '/mcp': {
          target: 'http://localhost:8229',
          configure: configureApiProxy,
        },
      },
    },
})
