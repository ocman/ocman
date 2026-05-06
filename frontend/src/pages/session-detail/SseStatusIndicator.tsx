import { useEffect, useState } from 'react';

export interface SseStatusIndicatorProps {
  /** True while the EventSource is OPEN. */
  active: boolean;
  /** True between an `onerror` and the subsequent successful open. */
  reconnecting: boolean;
  /** Consecutive reconnect attempts since the last successful open. */
  attempt: number;
  /** When the next reconnect is scheduled (epoch ms), or null when no
   *  retry is pending. */
  nextRetryAt: number | null;
  /** Cancel the pending backoff timer and reconnect immediately. */
  onRetryNow: () => void;
}

/** Round milliseconds up to whole seconds; clamps to 0. The display
 *  countdown rounds up so it never reads "0s" before the retry
 *  actually fires — mirrors RateLimitBanner's UX. */
function secondsUntil(target: number | null): number {
  if (target === null) return 0;
  const ms = target - Date.now();
  if (ms <= 0) return 0;
  return Math.ceil(ms / 1000);
}

/**
 * Inline footer indicator that surfaces SSE connectivity below the
 * conversation. Three states:
 *
 *   1. healthy (active=true) — renders nothing.
 *   2. reconnecting after a disconnect — shows attempt count, a live
 *      countdown to the next retry (driven by an internal 1s timer),
 *      and a "Retry now" button that bypasses the backoff.
 *   3. cold-start / never-connected — falls back to the original
 *      "Live updates unavailable -- polling every 10s" hint.
 */
export function SseStatusIndicator({
  active,
  reconnecting,
  attempt,
  nextRetryAt,
  onRetryNow,
}: SseStatusIndicatorProps) {
  const [, force] = useState(0);

  // While reconnecting, tick once a second so the countdown stays
  // current. We don't store seconds in state directly — recomputing
  // from `nextRetryAt` and `Date.now()` on every render keeps the
  // rendered value in lock-step with the actual scheduled retry,
  // even if the backoff is updated under us.
  useEffect(() => {
    if (!reconnecting || nextRetryAt === null) return;
    const id = window.setInterval(() => force((n) => n + 1), 1000);
    return () => window.clearInterval(id);
  }, [reconnecting, nextRetryAt]);

  if (active) return null;

  if (reconnecting) {
    const remaining = secondsUntil(nextRetryAt);
    return (
      <div
        className="oc-sse-indicator oc-sse-indicator-reconnecting"
        role="status"
        data-testid="sse-reconnecting-indicator"
      >
        <span className="oc-sse-indicator-dot" aria-hidden="true" />
        <span>
          Reconnecting to live updates
          {attempt > 0 ? ` (attempt ${attempt})` : ''}
          {remaining > 0 ? ` — retrying in ${remaining}s` : '…'}
        </span>
        <button
          type="button"
          className="oc-sse-indicator-retry"
          onClick={onRetryNow}
        >
          Retry now
        </button>
      </div>
    );
  }

  // Cold-start: the EventSource hasn't opened yet but the port is
  // believed to be available. Fallback polling kicks in every 10s
  // (handled in useSessionSSE).
  return (
    <div className="oc-sse-indicator">
      Live updates unavailable -- polling every 10s
    </div>
  );
}
