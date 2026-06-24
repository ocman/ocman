import { useEffect, useState } from 'react';
import type { SessionNotice } from '../lib/api';
import { formatDuration } from '../lib/format';

interface RateLimitBannerProps {
  notice: SessionNotice;
}

/**
 * Compute the remaining milliseconds until `retryAt`, clamped to 0.
 * Pure helper — no hooks, safe to call anywhere.
 */
function remainingMs(retryAt: number): number {
  return retryAt > 0 ? Math.max(0, retryAt - Date.now()) : 0;
}

/**
 * Renders a warning banner when the session is blocked by a transient
 * provider condition. Shows the backend-normalized message, a live
 * countdown that ticks every second until the retry time, and the
 * attempt number when known.
 *
 * Platform-agnostic: consumes the normalized `SessionNotice` from
 * the API without inspecting the platform field.
 */
export function RateLimitBanner({ notice }: RateLimitBannerProps) {
  // Seed with the current remaining time; the interval below keeps
  // it ticking. When `retryAt` changes (new attempt), React
  // re-creates the component via the parent's key/conditional, so
  // the initializer runs again with the fresh value.
  const [remaining, setRemaining] = useState(() => remainingMs(notice.retryAt));

  useEffect(() => {
    if (!notice.retryAt) return;

    const id = window.setInterval(() => {
      const ms = remainingMs(notice.retryAt);
      setRemaining(ms);
      if (ms <= 0) window.clearInterval(id);
    }, 1000);

    return () => window.clearInterval(id);
  }, [notice.retryAt]);

  let title = 'Error';
  if (notice.kind === 'rate_limit') title = 'Rate limited';
  if (notice.kind === 'provider_overloaded') title = 'Provider overloaded';

  return (
    <div className="oc-rate-limit-banner" role="status" data-testid="rate-limit-banner">
      <i className="bi bi-hourglass-split" aria-hidden="true" />
      <span className="oc-rate-limit-body">
        <strong>{title}</strong>
        {' — '}
        {notice.message}
        {remaining > 0 && (
          <span className="oc-rate-limit-retry">
            {' · '}Retrying in ~{formatDuration(remaining)}
          </span>
        )}
        {notice.attempt > 0 && (
          <span className="oc-rate-limit-attempt">
            {' · '}attempt {notice.attempt}
          </span>
        )}
      </span>
    </div>
  );
}
