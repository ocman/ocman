import { useEffect, useState } from 'react';
import { UpstreamApiError } from '../../lib/upstreamApi';
import { Button } from '../Control';
import './RemoteErrorBanner.css';

interface RemoteErrorBannerProps {
  error: UpstreamApiError | Error;
  onRetry: () => void;
}

/**
 * RemoteErrorBanner renders an inline error with a retry button.
 *
 * - For rate_limited envelopes, the retry button is disabled and the
 *   banner ticks down to the reset time.
 * - For auth_required envelopes, the banner shows a `gh auth login` /
 *   `tea login add` hint.
 * - Everything else falls back to a generic message + retry.
 */
export function RemoteErrorBanner({ error, onRetry }: RemoteErrorBannerProps) {
  const env = 'envelope' in error ? error.envelope : null;
  const code = env?.error.code;

  // List endpoints wrap forge HTTP failures in a 502 upstream_status message.
  if (error instanceof UpstreamApiError && (
    error.status === 404 || env?.error.status === 404 || env?.error.upstreamStatus === 404 ||
    (code === 'upstream_status' && /: status 404$/.test(error.message))
  )) {
    return (
      <div className="oc-upstream-empty" role="status">
        <p>Repository or list unavailable. It may be empty, missing, or private. Check your forge credentials and repository access.</p>
        <Button type="button" variant="ghost" size="compact" onClick={onRetry} data-testid="remote-error-retry">
          Retry
        </Button>
      </div>
    );
  }

  if (code === 'rate_limited') {
    return <RateLimitBanner resetAt={env?.error.retryAfter} onRetry={onRetry} />;
  }
  if (code === 'auth_required') {
    return (
      <div className="oc-upstream-error oc-upstream-error-auth" role="alert">
        <strong>Not authenticated.</strong>{' '}
        <span>
          Run <code>gh auth login</code> (GitHub) or <code>tea login add</code> (Forgejo) and
          restart ocman.
        </span>
        <button type="button" onClick={onRetry} data-testid="remote-error-retry">
          Retry
        </button>
      </div>
    );
  }
  return (
    <div className="oc-upstream-error" role="alert">
      <span>{error.message || 'Request failed.'}</span>
      <button type="button" onClick={onRetry} data-testid="remote-error-retry">
        Retry
      </button>
    </div>
  );
}

function RateLimitBanner({ resetAt, onRetry }: { resetAt: string | undefined; onRetry: () => void }) {
  const [remaining, setRemaining] = useState(() => secondsUntil(resetAt));
  useEffect(() => {
    if (remaining <= 0) return;
    const id = window.setInterval(() => {
      setRemaining(secondsUntil(resetAt));
    }, 1000);
    return () => window.clearInterval(id);
  }, [resetAt, remaining]);

  const ready = remaining <= 0;
  return (
    <div className="oc-upstream-error oc-upstream-error-rate-limited" role="alert">
      <span>Rate limited. {ready ? 'Ready to retry.' : `Retry in ${formatCountdown(remaining)}.`}</span>
      <button type="button" disabled={!ready} onClick={onRetry} data-testid="remote-error-retry">
        Retry
      </button>
    </div>
  );
}

function secondsUntil(iso: string | undefined): number {
  if (!iso) return 30;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return 30;
  return Math.max(0, Math.floor((t - Date.now()) / 1000));
}

function formatCountdown(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m ${s.toString().padStart(2, '0')}s`;
}
