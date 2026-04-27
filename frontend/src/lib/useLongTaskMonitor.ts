import { useEffect, useSyncExternalStore } from 'react';

/**
 * Aggregate stats over the lifetime of the page session. The
 * monitor is intentionally cheap: a single PerformanceObserver with
 * a counter, no per-task storage, no rendering on every event.
 *
 * The display layer pulls a snapshot on its own polling cadence
 * (BackendStats already runs every 5s), so we don't need to push
 * updates to subscribers more frequently than that.
 */
export interface LongTaskStats {
  /** Number of long tasks observed since the page loaded. */
  count: number;
  /** Wall-clock duration in ms of the worst observed task. */
  maxMs: number;
  /** Wall-clock duration in ms of the most recent task. 0 = none yet. */
  lastMs: number;
  /** performance.now() timestamp of the most recent task. 0 = none yet. */
  lastAt: number;
}

const initial: LongTaskStats = { count: 0, maxMs: 0, lastMs: 0, lastAt: 0 };

// Module-level state. We keep this outside React so the observer
// can survive remounts (e.g. <StrictMode> double-invoking effects)
// without losing accumulated counts.
let stats: LongTaskStats = { ...initial };
let observer: PerformanceObserver | null = null;
const subscribers = new Set<(s: LongTaskStats) => void>();

/**
 * recordEntry is exported for tests and for any future caller that
 * wants to feed synthetic entries through the same accounting path
 * the real observer uses. Production code calls it via the
 * PerformanceObserver callback.
 */
export function recordEntry(entry: { duration: number; startTime: number }) {
  // Ignore zero-duration synthetic entries (some test browsers emit
  // them when running in headless mode). Real long tasks are
  // strictly > 50ms by definition.
  if (!Number.isFinite(entry.duration) || entry.duration <= 0) return;
  stats = {
    count: stats.count + 1,
    maxMs: Math.max(stats.maxMs, entry.duration),
    lastMs: entry.duration,
    lastAt: entry.startTime,
  };
  for (const fn of subscribers) fn(stats);
}

export function _subscribeForTests(fn: (s: LongTaskStats) => void) {
  subscribers.add(fn);
  return () => { subscribers.delete(fn); };
}

export function _resetForTests() {
  stats = { ...initial };
  subscribers.clear();
  observer?.disconnect();
  observer = null;
}

export function _peekForTests(): LongTaskStats {
  return stats;
}

/**
 * Starts (or returns the already-running) longtask observer. Safe to
 * call multiple times: the second invocation is a no-op. Returns a
 * disposer; calling it disconnects the observer and clears module
 * state. Only the most recent disposer actually disconnects — earlier
 * disposers become no-ops once a later start() has reused the
 * observer.
 */
export function startLongTaskObserver(): () => void {
  if (typeof PerformanceObserver === 'undefined') {
    return () => {};
  }
  // The 'longtask' entry type isn't supported everywhere (Safari
  // doesn't ship it as of 2024). Falling back to no-op rather than
  // throwing keeps the dashboard usable.
  const supported =
    typeof PerformanceObserver.supportedEntryTypes !== 'undefined' &&
    PerformanceObserver.supportedEntryTypes.includes('longtask');
  if (!supported) {
    return () => {};
  }

  if (observer) {
    return () => {
      observer?.disconnect();
      observer = null;
    };
  }

  try {
    observer = new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        recordEntry({ duration: entry.duration, startTime: entry.startTime });
      }
    });
    observer.observe({ entryTypes: ['longtask'] });
  } catch {
    // Some browsers throw on observe() when the entry type is
    // syntactically valid but disabled by policy. Treat as no-op.
    observer = null;
    return () => {};
  }
  return () => {
    observer?.disconnect();
    observer = null;
  };
}

// React's useSyncExternalStore needs a stable getSnapshot reference;
// stats is a module-level object that's only replaced (never mutated)
// inside recordEntry, so returning it directly is safe.
function getSnapshot(): LongTaskStats {
  return stats;
}

function subscribe(onStoreChange: () => void): () => void {
  subscribers.add(onStoreChange);
  return () => {
    subscribers.delete(onStoreChange);
  };
}

/**
 * useLongTaskMonitor starts the global longtask observer (idempotent)
 * and returns a stats snapshot that updates whenever a new long task
 * is recorded.
 *
 * Long tasks are main-thread work blocks > 50ms — i.e. exactly the
 * stalls that make the UI feel "stuck". Browsers emit these via
 * PerformanceObserver entryTypes=['longtask']. Not all browsers
 * support it (Safari doesn't); on unsupported browsers the hook
 * returns the zero stats and no-ops cleanly.
 *
 * The hook uses useSyncExternalStore to subscribe to the module-
 * level stats so React stays in sync without a setState-in-effect.
 * Starting the observer is a side effect, kept in a separate
 * useEffect so it runs once per mount (idempotent at the module
 * level — repeated calls don't create extra observers).
 */
export function useLongTaskMonitor(): LongTaskStats {
  useEffect(() => {
    const stop = startLongTaskObserver();
    return stop;
  }, []);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
