// Minimal service worker for ocman.
//
// Purpose: satisfy Chromium's PWA install criteria so the URL-bar
// install affordance and our in-app "Install" button become available.
// We deliberately do NOT cache anything here — ocman is an
// always-online dashboard and stale caches would mask live state from
// /api/* endpoints (OpenCode port discovery, Claude Code hooks, etc).
//
// `skipWaiting` + `clients.claim` keep updates instant: when a new
// build ships, every open tab and installed-PWA window starts using
// the new SW immediately on next reload, no "waiting" worker stuck
// from a previous version.
//
// The empty `fetch` listener exists only because Chrome's heuristic
// requires the service worker to handle fetch events; returning
// undefined lets the request fall through to the network as if no SW
// were installed.

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('fetch', () => {
  // Intentionally empty: pass through to network.
});
