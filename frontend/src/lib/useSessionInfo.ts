import { useApiStore } from './apiStore';
import type { SessionInfo } from './api';
import {
  useDebouncedSessionResource,
  type DebouncedSessionResourceOptions,
  type DebouncedSessionResourceResult,
} from './useDebouncedSessionResource';

// Stable empty payload returned when the hook is disabled or the
// platform doesn't expose this data. Reused across renders so
// referential equality holds; downstream effects keying off `data`
// won't churn.
const EMPTY_INFO: SessionInfo = {
  sessionId: '',
  supported: false,
  context: { tokens: 0, cost: 0, estCost: 0 },
  tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  mcpServers: [],
  lspServers: [],
  messages: { user: 0, assistant: 0 },
};

export type UseSessionInfoOptions = DebouncedSessionResourceOptions;
export type UseSessionInfoResult = DebouncedSessionResourceResult<SessionInfo>;

/**
 * useSessionInfo fetches /api/session/{id}/info and re-fetches
 * whenever `options.dirtyTick` changes. Refetch timing follows
 * DebouncedTrigger: a 500 ms inner debounce that resets on every
 * tick, with a 5 s hard ceiling so a continuously-busy session still
 * sees periodic updates.
 *
 * Returns `{ data, loading, error, refresh }`. When `enabled` is
 * false, returns a synchronous EMPTY_INFO with `loading: false` so
 * the caller can render a "Not supported" empty state without a
 * flash of skeleton.
 */
export function useSessionInfo(
  sessionId: string | undefined,
  options: UseSessionInfoOptions = {},
): UseSessionInfoResult {
  const getSessionInfo = useApiStore((s) => s.getSessionInfo);
  return useDebouncedSessionResource(
    sessionId,
    getSessionInfo,
    EMPTY_INFO,
    'Failed to load session info',
    options,
  );
}
