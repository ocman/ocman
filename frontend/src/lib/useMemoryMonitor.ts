import { useEffect } from 'react';

// `performance.memory` is a Chromium-only extension that isn't in
// lib.dom.d.ts. `window.gc` is only present when the page was launched with
// `--js-flags=--expose-gc`. We narrow to explicit types instead of casting
// to `any` at each site.
interface PerformanceMemory {
  usedJSHeapSize: number;
  totalJSHeapSize: number;
  jsHeapSizeLimit: number;
}
interface PerformanceWithMemory extends Performance {
  memory?: PerformanceMemory;
}
interface WindowWithGC extends Window {
  gc?: () => void;
}

/**
 * Monitors frontend memory usage and forces garbage collection
 * when memory exceeds thresholds.
 *
 * This helps prevent OOM in long-running sessions by:
 * 1. Monitoring JS heap usage every 30 seconds
 * 2. Triggering manual GC when memory exceeds 800MB
 * 3. Logging warnings when approaching limits
 */
export function useMemoryMonitor() {
  useEffect(() => {
    // Only run if performance.memory API is available (Chromium-based browsers)
    if (!('memory' in performance)) {
      return;
    }

    const check = () => {
      const memory = (performance as PerformanceWithMemory).memory;
      if (!memory) return;

      const usedMB = memory.usedJSHeapSize / (1024 * 1024);
      const limitMB = memory.jsHeapSizeLimit / (1024 * 1024);
      const percentage = (usedMB / limitMB) * 100;

      // Warn at 70% of browser's JS heap limit
      if (percentage > 70) {
        console.warn(
          `[Memory] High usage: ${usedMB.toFixed(0)}MB / ${limitMB.toFixed(0)}MB (${percentage.toFixed(0)}% of heap limit)`
        );
      }

      // Force GC at 80% of heap limit if available (Chrome with --js-flags=--expose-gc)
      const gc = (window as WindowWithGC).gc;
      if (percentage > 80 && typeof gc === 'function') {
        console.warn(
          `[Memory] Triggering manual GC at ${usedMB.toFixed(0)}MB (${percentage.toFixed(0)}% of ${limitMB.toFixed(0)}MB limit)`
        );
        try {
          gc();
        } catch (err) {
          console.debug('[Memory] GC call failed:', err);
        }
      }
    };

    // Check every 30 seconds
    const interval = setInterval(check, 30_000);
    check(); // Initial check

    return () => clearInterval(interval);
  }, []);
}
