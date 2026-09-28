import { create } from 'zustand';

/**
 * Global "backend unreachable" flag. api.ts sets it on network failure or a
 * gateway 5xx and clears it on the next successful response; the global SSE
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

export function markBackendReachable(): void {
  if (!useBackendStatus.getState().unreachable) return;
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
}
