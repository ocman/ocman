import { useEffect, useState } from 'react';
import { Button } from './Control';
import type { SessionNotice } from '../lib/api';
import { formatDuration } from '../lib/format';

interface RateLimitBannerProps {
  notice: SessionNotice;
  onChangeModel?: () => void;
}

/**
 * Compute the remaining milliseconds until `retryAt`, clamped to 0.
 * Pure helper — no hooks, safe to call anywhere.
 */
function remainingMs(retryAt: number): number {
  return retryAt > 0 ? Math.max(0, retryAt - Date.now()) : 0;
}

/**
 * Surfaces a transient provider condition (rate limit, overload,
 * error) inline above the composer. Shows
 * the backend-normalized message, a live countdown that ticks every
 * second until the retry time, and the attempt number when known.
 *
 * Platform-agnostic: consumes the normalized `SessionNotice` from
 * the API without inspecting the platform field.
 */
export function RateLimitBanner({ notice, onChangeModel }: RateLimitBannerProps) {
  const [, tick] = useState(0);
  const remaining = remainingMs(notice.retryAt);

  useEffect(() => {
    if (!notice.retryAt) return;

    const id = window.setInterval(() => {
      const ms = remainingMs(notice.retryAt);
      tick((value) => value + 1);
      if (ms <= 0) window.clearInterval(id);
    }, 1000);

    return () => window.clearInterval(id);
  }, [notice.retryAt]);

  let title = 'Error';
  if (notice.kind === 'retry') title = 'Retrying';
  if (notice.kind === 'rate_limit') title = 'Rate limited';
  if (notice.kind === 'provider_overloaded') title = 'Provider overloaded';
  if (notice.kind === 'model_switch') title = 'Switched model';
  if (notice.kind === 'models_exhausted') title = 'All models cooled down';

  const retryText = remaining > 0
    ? ` · ${notice.kind === 'models_exhausted' ? 'Earliest recovers in' : 'Retrying in'} ~${formatDuration(remaining)}`
    : '';
  const attemptText = notice.attempt > 0 ? ` (${notice.attempt}/n)` : '';

  return (
    <div className={`oc-sse-indicator oc-sse-indicator-reconnecting${title === 'Error' ? ' oc-sync-indicator-failed' : ''}`} role={title === 'Error' ? 'alert' : 'status'} data-testid="rate-limit-banner">
      <span title={`${title} — ${notice.message}${retryText}${attemptText}`}>
        <i className="bi bi-hourglass-split" aria-hidden="true" />
        {' '}
        <strong>{title}</strong>
        {' — '}
        {notice.message}
      </span>
      {remaining > 0 && (
        <span className="oc-rate-limit-retry" title={retryText.trim()}>
          {retryText}
        </span>
      )}
      {notice.attempt > 0 && (
        <span className="oc-rate-limit-attempt" title={attemptText.trim()}>
          {attemptText}
        </span>
      )}
      {notice.kind === 'rate_limit' && onChangeModel && (
        <>
          <span title="Try another model to continue.">Try another model to continue.</span>
          <Button variant="link" size="compact" onClick={onChangeModel}>Change model</Button>
        </>
      )}
    </div>
  );
}
