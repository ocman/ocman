/**
 * Fires when the user plausibly comes back after the live streams may
 * have silently died: the tab becomes visible after being hidden for a
 * while, the browser comes back online, or the wall clock jumps (the
 * machine slept with the tab visible).
 *
 * Sleep and network changes can leave an EventSource half-open — it
 * still reports open, no error fires, and events broadcast meanwhile
 * are never delivered. Subscribers replace their streams on this
 * signal, which runs their usual reconnect reconciliation.
 */

/** Shorter absences keep the stream; the server keepalive is 25s. */
export const RESUME_AFTER_MS = 30_000;
const CLOCK_TICK_MS = 10_000;

const listeners = new Set<() => void>();
let hiddenAt: number | null = null;
let lastTick = 0;
let clock: ReturnType<typeof setInterval> | null = null;

function fire(): void {
  for (const cb of [...listeners]) cb();
}

function onVisibility(): void {
  if (document.hidden) {
    hiddenAt = Date.now();
    return;
  }
  const away = hiddenAt === null ? 0 : Date.now() - hiddenAt;
  hiddenAt = null;
  lastTick = Date.now();
  if (away >= RESUME_AFTER_MS) fire();
}

function onTick(): void {
  const now = Date.now();
  const gap = now - lastTick;
  lastTick = now;
  // Hidden tabs are throttled; visibilitychange covers those.
  if (!document.hidden && gap >= CLOCK_TICK_MS + RESUME_AFTER_MS) fire();
}

/** Subscribe to resume signals. Returns the unsubscribe function. */
export function onPageResume(cb: () => void): () => void {
  listeners.add(cb);
  if (listeners.size === 1) {
    hiddenAt = document.hidden ? Date.now() : null;
    lastTick = Date.now();
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('online', fire);
    clock = setInterval(onTick, CLOCK_TICK_MS);
  }
  return () => {
    if (!listeners.delete(cb) || listeners.size > 0) return;
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('online', fire);
    if (clock) clearInterval(clock);
    clock = null;
  };
}
