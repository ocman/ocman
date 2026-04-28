// Minimal service worker for ocman.
//
// Purpose: satisfy Chromium's PWA install criteria so the URL-bar
// install affordance and our in-app "Install" button become available,
// and route notification clicks back to the right session.
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

// Notification click routing.
//
// Notifications are constructed in-page by useNotificationNotify with a
// `data: { url }` payload pointing at /session/<id>. When the user
// clicks the toast we either focus an existing ocman window (and ask it
// to navigate via postMessage) or open a fresh one if the PWA isn't
// running. This is the path that matters for the installed-PWA case
// where the user closed the app entirely — without it, the click would
// just dismiss the toast and do nothing.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = event.notification.data && event.notification.data.url;
  if (!url) return;

  event.waitUntil((async () => {
    const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    // Prefer an existing client on the same origin — focus it and ask
    // it to navigate. We post a message instead of forcing a hard
    // navigation so the SPA's routing keeps client-side state (open
    // panels, scroll position elsewhere) where reasonable.
    for (const client of all) {
      try {
        const clientUrl = new URL(client.url);
        const target = new URL(url, clientUrl.origin);
        if (clientUrl.origin !== target.origin) continue;
        await client.focus();
        client.postMessage({ type: 'ocman:navigate', url: target.pathname + target.search });
        return;
      } catch {
        // Bad URL on a client — skip and try the next one.
      }
    }
    // No client open — launch a new window at the deep link directly.
    if (self.clients.openWindow) {
      await self.clients.openWindow(url);
    }
  })());
});
