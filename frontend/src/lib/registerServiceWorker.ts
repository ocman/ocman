/**
 * Register the PWA service worker. Called once from main.tsx.
 *
 * Only runs in production builds — in dev (`make dev`, vite's HMR
 * server on :8228) we deliberately skip registration so cached
 * assets can't shadow a freshly-edited bundle. The service worker
 * itself is a network-passthrough (see public/sw.js), so even when
 * registered it doesn't cache anything; gating on PROD is belt-and-
 * braces.
 *
 * The registration is best-effort: any failure (HTTP error, browser
 * without SW support, cross-origin shenanigans behind a quirky proxy)
 * is logged at debug level and the app keeps working as a normal web
 * app. PWA install is purely additive.
 */
export function registerServiceWorker(): void {
  if (!import.meta.env.PROD) return;
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return;

  // Defer until after load so SW registration never competes with the
  // initial render for network/CPU.
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch((err) => {
      console.debug('[ocman] service worker registration failed:', err);
    });
  });
}
