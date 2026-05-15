import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource/jetbrains-mono/latin-400.css'
import '@fontsource/jetbrains-mono/latin-600.css'
import '@fontsource/jetbrains-mono/latin-700.css'
import 'bootstrap-icons/font/bootstrap-icons.css'
import 'highlight.js/styles/github-dark-dimmed.min.css'
import './tokens.css'
import App from './App'
import { installRemoteLogHandlers } from './lib/remoteLog'
import { installAuthIntegration } from './lib/authStore'
import { registerServiceWorker } from './lib/registerServiceWorker'

// When running inside a Wails desktop window, the Go runtime injects
// `window.runtime`. Tag <body> with `wails-app` so CSS can apply
// platform-specific styles (traffic-light clearance, drag region, etc.)
// without any build-time branching.
if (typeof window !== 'undefined' && 'runtime' in window) {
  document.body.classList.add('wails-app')
}

// Install global error -> /api/debug/log handlers before the app boots, so
// any render-time crash is captured on the server log too.
installRemoteLogHandlers()

// Wire the auth store to the api layer so a 401 from any fetch flips
// the client into the lockscreen. Must run before the first request
// — App's effect-based bootstrap fires the first one.
installAuthIntegration()

// Register the PWA service worker (production builds only). Enables
// the browser's install affordance + our in-app Install button.
// No-op in dev mode and on browsers without SW support.
registerServiceWorker()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
