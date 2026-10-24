import { useSyncExternalStore } from 'react';

// Long tasks are main-thread blocks > 50ms, i.e. the stalls that make
// the UI feel stuck. One module-level observer starts on import and
// counts them for the page's lifetime. Browsers without 'longtask'
// support (Safari) leave the stats at zero.
export interface LongTaskStats {
  /** Number of long tasks observed since the page loaded. */
  count: number;
  /** Wall-clock duration in ms of the worst observed task. */
  maxMs: number;
}

let stats: LongTaskStats = { count: 0, maxMs: 0 };
const listeners = new Set<() => void>();

if (typeof PerformanceObserver !== 'undefined'
  && PerformanceObserver.supportedEntryTypes?.includes('longtask')) {
  try {
    new PerformanceObserver((list) => {
      for (const { duration } of list.getEntries()) {
        // Some headless browsers emit zero-duration synthetic entries.
        if (!Number.isFinite(duration) || duration <= 0) continue;
        stats = { count: stats.count + 1, maxMs: Math.max(stats.maxMs, duration) };
      }
      for (const listener of listeners) listener();
    }).observe({ entryTypes: ['longtask'] });
  } catch {
    // Some browsers throw when the entry type is disabled by policy.
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

function getSnapshot() {
  return stats;
}

export function useLongTaskMonitor(): LongTaskStats {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
