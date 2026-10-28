import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
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
  proxy.on('proxyRes', (proxyRes, _req, res) => {
    // Air can kill the backend after SSE headers have been forwarded.
    // Propagate that abort or the browser waits forever instead of retrying.
    proxyRes.on('aborted', () => res.destroy())
    proxyRes.on('data', () => {})
  })
}

// Shared by the dev and preview servers.
const apiProxy: Record<string, ProxyOptions> = {
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
}

// Host singletons served to plugin UI bundles at stable, unhashed URLs
// (/plugin-shared/<name>.js), in production and in the dev server alike.
// sdk/plugin-vite maps the same bare specifiers to these URLs.
const pluginSharedEntries = Object.fromEntries(
  ['react', 'react-jsx-runtime', 'react-dom', 'react-router-dom', 'tanstack-react-query', 'ocman-plugin-ui']
    .map((name) => [`plugin-shared/${name}`, `plugin-shared/${name}.js`]),
)

// CommonJS packages lose their named exports through `export *`, so these
// entries are generated with the package's actual export names.
const commonJSShared: Record<string, string> = { react: 'react', 'react-jsx-runtime': 'react/jsx-runtime', 'react-dom': 'react-dom' }

function pluginSharedCommonJS(): Plugin {
  const dir = fileURLToPath(new URL('./plugin-shared/', import.meta.url))
  const require = createRequire(import.meta.url)
  return {
    name: 'ocman-plugin-shared-commonjs',
    enforce: 'pre',
    load(id) {
      const file = id.split('?')[0]
      if (path.dirname(file) + path.sep !== dir) return
      const specifier = commonJSShared[path.basename(file, '.js')]
      if (!specifier) return
      const names = Object.keys(require(specifier) as object).filter((n) => n !== 'default' && /^[A-Za-z_$][\w$]*$/.test(n))
      return `import m from '${specifier}';\nexport const { ${names.join(', ')} } = m;\nexport default m;\n`
    },
  }
}

// OCMAN_PROFILE=1 builds against React's profiling renderer so <Profiler>
// reports commit timings in a production bundle (used by `pnpm perf`).
const profileBuild = process.env.OCMAN_PROFILE === '1'

export default defineConfig({
  plugins: [
    pluginSharedCommonJS(),
    react(),
    // React Compiler — auto-memoizes components and hooks at build
    // time so we don't have to remember useMemo/useCallback for every
    // prop/store-adapter object. Catches the class of "unstable
    // reference passed to a hook running an unconditional effect"
    // bugs that have plagued this codebase (see commits a1d1140 etc).
    babel({ presets: [reactCompilerPreset()] }),
  ],
  resolve: profileBuild ? { alias: { 'react-dom/client': 'react-dom/profiling' } } : undefined,
  build: {
    // The go:embed source; the desktop app serves the same embedded assets.
    outDir: '../internal/webui/static',
    emptyOutDir: true,
    rollupOptions: {
      input: { index: 'index.html', ...pluginSharedEntries },
      // Plugin bundles import these entries by URL, so their exports must
      // survive tree-shaking and share the app's single module instances.
      preserveEntrySignatures: 'exports-only',
      output: {
        entryFileNames: (chunk) => chunk.name.startsWith('plugin-shared/') ? '[name].js' : 'assets/[name]-[hash].js',
      },
    },
  },
  server: {
    // Tailscale Serve owns :8228 on the tailnet address; bind its upstream to loopback.
    host: '127.0.0.1',
    port: 8228,
    strictPort: true,
    allowedHosts: extraAllowedHosts,
    proxy: apiProxy,
  },
  preview: {
    host: '127.0.0.1',
    port: 8228,
    strictPort: true,
    allowedHosts: extraAllowedHosts,
    proxy: apiProxy,
  },
})
