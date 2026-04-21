import { useEffect } from 'react';

/**
 * Periodically clears accumulated browser performance entries to prevent memory leaks.
 * 
 * React 19 development mode creates PerformanceMeasure entries during renders for profiling.
 * React DevTools can also inject performance tracking even in production builds.
 * In long-running sessions with polling/intervals, these can accumulate to 100s of MB.
 * 
 * This hook only runs in development mode (or when DevTools is detected) and clears
 * performance entries every 60 seconds, keeping only the most recent for debugging.
 */
export function usePerformanceCleanup(intervalMs: number = 60_000) {
  useEffect(() => {
    // Only run if we're in dev mode OR React DevTools is present
    const isDev = import.meta.env.DEV;
    const hasDevTools = typeof window !== 'undefined' && 
                        '__REACT_DEVTOOLS_GLOBAL_HOOK__' in window;
    
    if (!isDev && !hasDevTools) {
      return; // No cleanup needed in prod without DevTools
    }

    const cleanup = () => {
      try {
        // Keep only the last 100 entries of each type for debugging
        const entries = performance.getEntries();
        if (entries.length > 100) {
          performance.clearMarks();
          performance.clearMeasures();
          performance.clearResourceTimings();
          
          // Re-add a marker so we know cleanup happened
          performance.mark('ocman:perf-cleanup');
        }
      } catch (err) {
        // Ignore errors in cleanup - not critical
        console.debug('Performance cleanup failed:', err);
      }
    };

    const id = setInterval(cleanup, intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
}
