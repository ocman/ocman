import { useState, useEffect, useRef } from 'react';
import { useApiStore } from '../lib/apiStore';
import { useLongTaskMonitor } from '../lib/useLongTaskMonitor';
import './BackendStats.css';

// `performance.memory` is a non-standard Chromium-only extension, so it isn't
// in lib.dom.d.ts. We narrow `performance` to a type that exposes it instead
// of casting to `any` at each read site.
interface PerformanceMemory {
  usedJSHeapSize: number;
  totalJSHeapSize: number;
  jsHeapSizeLimit: number;
}
interface PerformanceWithMemory extends Performance {
  memory?: PerformanceMemory;
}

export function BackendStats() {
  const [backendMemory, setBackendMemory] = useState<number | null>(null);
  const [uptime, setUptime] = useState<number | null>(null);
  const [frontendMemory, setFrontendMemory] = useState<number | null>(null);
  const longTasks = useLongTaskMonitor();
  const getSystemStats = useApiStore((s) => s.getSystemStats);

  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const load = () => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      getSystemStats(controller.signal)
        .then((stats) => {
          if (controller.signal.aborted) return;
          setBackendMemory(stats.memory.heapAlloc);
          setUptime(stats.uptime);
        })
        .catch(() => {
          if (controller.signal.aborted) return;
          setBackendMemory(null);
          setUptime(null);
        })
        .finally(() => {
          if (controller.signal.aborted) return;
          // Update frontend memory if available
          const memory = (performance as PerformanceWithMemory).memory;
          if (memory) {
            setFrontendMemory(memory.usedJSHeapSize);
          }
        });
    };
    load(); // Initial load
    const interval = setInterval(load, 5000);
    return () => {
      clearInterval(interval);
      abortRef.current?.abort();
    };
  }, [getSystemStats]);

  if (backendMemory === null) return null;

  // Calculate memory percentage and warning level
  const getMemoryWarning = (): { level: 'ok' | 'warning' | 'critical', percentage?: number } => {
    if (frontendMemory) {
      const memory = (performance as PerformanceWithMemory).memory;
      if (memory) {
        const percentage = (frontendMemory / memory.jsHeapSizeLimit) * 100;
        if (percentage > 80) return { level: 'critical', percentage };
        if (percentage > 70) return { level: 'warning', percentage };
        return { level: 'ok', percentage };
      }
    }
    return { level: 'ok' };
  };

  const memoryWarning = getMemoryWarning();

  // Long-task severity. The longtask API only fires for main-thread
  // blocks > 50ms, so any non-zero count is by definition a stall.
  // Tier the colouring so a single 60ms blip doesn't scream the same
  // way as a 500ms freeze does.
  const getLongTaskSeverity = (): 'ok' | 'warning' | 'critical' => {
    if (longTasks.count === 0) return 'ok';
    if (longTasks.maxMs >= 250) return 'critical';
    if (longTasks.maxMs >= 100) return 'warning';
    return 'ok';
  };
  const longTaskSeverity = getLongTaskSeverity();

  const formatUptime = (seconds: number): string => {
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    const secs = Math.floor(seconds % 60);
    
    if (hours > 0) {
      return `${hours}h ${minutes}m`;
    } else if (minutes > 0) {
      return `${minutes}m ${secs}s`;
    }
    return `${secs}s`;
  };

  return (
    <div className="backend-stats">
      <span className="backend-stats-item">
        <span className="backend-stats-label" title="Backend Memory">be</span>: {(backendMemory / (1024 * 1024)).toFixed(0)}MB
      </span>
      {frontendMemory !== null && (
        <span 
          className={`backend-stats-item ${memoryWarning.level !== 'ok' ? `memory-${memoryWarning.level}` : ''}`}
          title={memoryWarning.percentage 
            ? `Frontend Memory (${memoryWarning.percentage.toFixed(0)}% of heap limit)` 
            : 'Frontend Memory'}
        >
          <span className="backend-stats-label" title="Frontend Memory">fe</span>: {(frontendMemory / (1024 * 1024)).toFixed(0)}MB
        </span>
      )}
      {longTasks.count > 0 && (
        <span
          className={`backend-stats-item backend-stats-longtasks${longTaskSeverity !== 'ok' ? ` longtasks-${longTaskSeverity}` : ''}`}
          title={`Long tasks (>50ms main-thread blocks). Worst: ${longTasks.maxMs.toFixed(0)}ms`}
        >
          <span className="backend-stats-label" title="Long tasks (main-thread stalls > 50ms)">lt</span>: {longTasks.count}
          {longTasks.maxMs > 0 && ` / ${longTasks.maxMs.toFixed(0)}ms`}
        </span>
      )}
      {uptime !== null && (
        <span className="backend-stats-item backend-stats-uptime">
          <span className="backend-stats-label" title="Uptime">up</span>: {formatUptime(uptime)}
        </span>
      )}
    </div>
  );
}
