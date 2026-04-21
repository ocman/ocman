import { useState, useEffect } from 'react';
import { useApiStore } from '../lib/apiStore';
import './BackendStats.css';

export function BackendStats() {
  const [backendMemory, setBackendMemory] = useState<number | null>(null);
  const [uptime, setUptime] = useState<number | null>(null);
  const [frontendMemory, setFrontendMemory] = useState<number | null>(null);
  const getSystemStats = useApiStore((s) => s.getSystemStats);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const stats = await getSystemStats();
        if (!cancelled) {
          setBackendMemory(stats.memory.heapAlloc);
          setUptime(stats.uptime);
        }
      } catch (err) {
        // Silently ignore errors - this is just nice-to-have info
        if (!cancelled) {
          setBackendMemory(null);
          setUptime(null);
        }
      }

      // Update frontend memory if available
      if (!cancelled && 'memory' in performance) {
        const memory = (performance as any).memory;
        if (memory) {
          setFrontendMemory(memory.usedJSHeapSize);
        }
      }
    };
    load(); // Initial load
    const interval = setInterval(load, 5000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [getSystemStats]);

  if (backendMemory === null) return null;

  // Calculate memory percentage and warning level
  const getMemoryWarning = (): { level: 'ok' | 'warning' | 'critical', percentage?: number } => {
    if (frontendMemory && 'memory' in performance) {
      const memory = (performance as any).memory;
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
      {uptime !== null && (
        <span className="backend-stats-item">
          <span className="backend-stats-label" title="Uptime">up</span>: {formatUptime(uptime)}
        </span>
      )}
    </div>
  );
}
