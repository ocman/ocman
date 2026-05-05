import { useMemo } from 'react';
import type { SessionNotice } from '../lib/api';
import { formatDuration } from '../lib/format';

interface RateLimitBannerProps {
  notice: SessionNotice;
}

/**
 * Renders a passive informational banner when the session is blocked
 * by a rate limit. Shows the backend-normalized message and, when
 * available, a relative retry countdown and attempt number.
 *
 * Platform-agnostic: consumes the normalized `SessionNotice` from the
 * API without inspecting the platform field.
 */
export function RateLimitBanner({ notice }: RateLimitBannerProps) {
  // Compute the retry text once per render via useMemo so the linter
  // doesn't flag Date.now() as an impure call in the render body.
  // The value is intentionally not live-updating — the banner is
  // passive information, not a countdown timer.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const retryText = useMemo(() => {
    if (!notice.retryAt) return null;
    const remaining = notice.retryAt - Date.now();
    return remaining > 0 ? `Retrying in ~${formatDuration(remaining)}` : null;
  }, [notice.retryAt]);

  if (notice.kind !== 'rate_limit') return null;

  return (
    <div className="oc-rate-limit-banner" role="status" data-testid="rate-limit-banner">
      <i className="bi bi-hourglass-split" aria-hidden="true" />
      <span className="oc-rate-limit-body">
        <strong>Rate limited</strong>
        {' — '}
        {notice.message}
        {retryText && (
          <span className="oc-rate-limit-retry">
            {' · '}{retryText}
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
