import { useEffect, useState } from 'react';

/**
 * Wall-clock active duration to display: the reported value, ticking
 * up once a second while the turn is running.
 */
export function useRunningDuration(activeDurationMs: number | undefined, isRunning: boolean): number {
  const [displayDurationMs, setDisplayDurationMs] = useState(activeDurationMs ?? 0);
  useEffect(() => {
    const baseDurationMs = activeDurationMs ?? 0;
    if (!isRunning) return;

    const startedAt = Date.now();
    const updateDuration = () => {
      setDisplayDurationMs(baseDurationMs + Date.now() - startedAt);
    };
    const initialTimer = window.setTimeout(updateDuration, 0);
    const timer = window.setInterval(updateDuration, 1_000);
    return () => {
      window.clearTimeout(initialTimer);
      window.clearInterval(timer);
    };
  }, [activeDurationMs, isRunning]);
  return isRunning ? displayDurationMs : (activeDurationMs ?? 0);
}
