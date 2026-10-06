import { create } from 'zustand';

/**
 * Global "backend unreachable" flag. api.ts sets it on repeated network
 * failure (see reportNetworkFailure) or a gateway 5xx and clears it on the
 * next successful response; the global SSE
 * stream sets it once its reconnect backoff reaches 5s and clears it on open.
 */
type BackendStatus = {
  unreachable: boolean;
  since: number | null;
  /** Last failure detail, shown under the banner. */
  error: string | null;
};

export const useBackendStatus = create<BackendStatus>(() => ({
  unreachable: false,
  since: null,
  error: null,
}));

export function markBackendUnreachable(error: string): void {
  const s = useBackendStatus.getState();
  if (s.unreachable && s.error === error) return;
  useBackendStatus.setState({ unreachable: true, since: s.since ?? Date.now(), error });
}

// A single failed fetch is weak evidence: Safari fails in-flight requests
// with "Load failed" when the tab is backgrounded or the device wakes, and
// one dropped connection over Tailscale looks the same. Network failures
// therefore only raise the flag when the page is visible, outside a short
// grace window after it became visible again, and on the second failure in
// a row. A gateway 502/504 is a real answer and still flags immediately.
const RESUME_GRACE_MS = 5_000;
const FAILURES_TO_FLAG = 2;
let networkFailures = 0;
let resumedAt = 0;

if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') resumedAt = Date.now();
  });
}

export function reportNetworkFailure(error: string): void {
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
  if (Date.now() - resumedAt < RESUME_GRACE_MS) return;
  if (++networkFailures < FAILURES_TO_FLAG) return;
  markBackendUnreachable(error);
}

export function markBackendReachable(): void {
  networkFailures = 0;
  if (!useBackendStatus.getState().unreachable) return;
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
}
