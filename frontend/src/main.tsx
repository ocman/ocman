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

// Install global error -> /api/debug/log handlers before the app boots, so
// any render-time crash is captured on the server log too.
installRemoteLogHandlers()

// Wire the auth store to the api layer so a 401 from any fetch flips
// the client into the lockscreen. Must run before the first request
// — App's effect-based bootstrap fires the first one.
installAuthIntegration()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
