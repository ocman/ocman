import { Profiler, type ReactNode } from 'react';
import { onProfilerRender, perfEnabled } from '../lib/perfMonitor';

/**
 * React Profiler around a subtree, only when perf monitoring is on
 * (`?debug` / localStorage `ocman:perf`). Otherwise renders children as-is.
 * Production builds report timings only when built with OCMAN_PROFILE=1
 * (aliases react-dom/client to react-dom/profiling, see vite.config.ts).
 */
export function PerfProfiler({ id, children }: { id: string; children: ReactNode }) {
  if (!perfEnabled()) return children;
  return <Profiler id={id} onRender={onProfilerRender}>{children}</Profiler>;
}
