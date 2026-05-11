import { useApiStore } from './apiStore';
import type { SessionChanges } from './api';
import {
  useDebouncedSessionResource,
  type DebouncedSessionResourceOptions,
  type DebouncedSessionResourceResult,
} from './useDebouncedSessionResource';

// Stable empty payload used when the hook is disabled or the platform
// doesn't support file-change aggregation. Stable identity so
// re-renders don't churn referential equality.
const EMPTY_CHANGES: SessionChanges = {
  sessionId: '',
  supported: false,
  totalAdditions: 0,
  totalDeletions: 0,
  filesChanged: 0,
  files: [],
};

export type UseSessionChangesOptions = DebouncedSessionResourceOptions;
export type UseSessionChangesResult = DebouncedSessionResourceResult<SessionChanges>;

/**
 * useSessionChanges fetches /api/session/{id}/changes and re-fetches
 * whenever `options.dirtyTick` changes. Refetch timing follows
 * DebouncedTrigger: a 500 ms inner debounce that resets on every
 * tick, with a 5 s hard ceiling so a continuously-busy session still
 * sees periodic updates.
 *
 * Returns `{ data, loading, error, refresh }`. When `enabled` is
 * false, returns a synchronous EMPTY_CHANGES with `loading: false`
 * so the caller can render a "Not supported" empty state without a
 * flash of skeleton.
 */
export function useSessionChanges(
  sessionId: string | undefined,
  options: UseSessionChangesOptions = {},
): UseSessionChangesResult {
  const getSessionChanges = useApiStore((s) => s.getSessionChanges);
  return useDebouncedSessionResource(
    sessionId,
    getSessionChanges,
    EMPTY_CHANGES,
    'Failed to load changes',
    options,
  );
}
