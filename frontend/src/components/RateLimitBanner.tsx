import { useEffect, useState } from 'react';
import * as Toast from '@radix-ui/react-toast';
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
 * error) as an auto-hiding toast in the shared toast viewport. Shows
 * the backend-normalized message, a live countdown that ticks every
 * second until the retry time, and the attempt number when known.
 * A dismissed/expired toast re-surfaces when the notice changes
 * (new kind, retry time, or message).
 *
 * Platform-agnostic: consumes the normalized `SessionNotice` from
 * the API without inspecting the platform field.
 */
export function RateLimitBanner({ notice, onChangeModel }: RateLimitBannerProps) {
  const [remaining, setRemaining] = useState(() => remainingMs(notice.retryAt));
  const [dismissedKey, setDismissedKey] = useState<string | null>(null);
  const noticeKey = `${notice.kind}:${notice.retryAt}:${notice.message}`;

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
    <Toast.Root
      key={noticeKey}
      className={`oc-toast-root ${title === 'Error' ? 'error' : 'warning'}`}
      open={dismissedKey !== noticeKey}
      onOpenChange={(open) => { if (!open) setDismissedKey(noticeKey); }}
      duration={10000}
    >
      <Toast.Description className="oc-toast-description" data-testid="rate-limit-banner">
        <div className="oc-toast-body">
          <span>
            <i className="bi bi-hourglass-split" aria-hidden="true" />
            {' '}
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
          {notice.kind === 'rate_limit' && onChangeModel && (
            <>
              <span>Try another model to continue.</span>
              <Toast.Action asChild altText="Change model" onClick={onChangeModel}>
                <button type="button" className="oc-toast-action">Change model</button>
              </Toast.Action>
            </>
          )}
        </div>
      </Toast.Description>
    </Toast.Root>
  );
}
