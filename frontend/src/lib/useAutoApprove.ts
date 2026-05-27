import { useCallback, useEffect, useState } from 'react';
import { useApiStore } from './apiStore';
import { useUiStore } from './uiStore';

export interface UseAutoApproveOptions {
  sessionId: string;
  /** Whether the platform supports auto-approve at all. */
  capable: boolean;
}

export interface UseAutoApproveResult {
  /** Whether auto-approve is currently enabled for this session. */
  enabled: boolean;
  /** Error message from enabling/disabling, if any. */
  toggleError: string | null;
  /** Enable or disable auto-approve for this session. */
  setEnabled: (on: boolean) => Promise<void>;
}

/**
 * Manages per-session auto-approve enabled/disabled state.
 *
 * The hook fetches the current state from the server on mount and
 * persists changes. The actual judge logic, "checking" indicator, and
 * approval notices are all driven server-side and reflected through
 * the session reducer via `ocman.permission.checking` and
 * `ocman.permission.auto-approved` SSE events.
 */
export function useAutoApprove({
  sessionId,
  capable,
}: UseAutoApproveOptions): UseAutoApproveResult {
  const getAutoApprove = useApiStore((s) => s.getAutoApprove);
  const setAutoApproveApi = useApiStore((s) => s.setAutoApprove);

  // Global default from settings — applied when no per-session override exists.
  const autoApproveDefault = useUiStore((s) => s.autoApproveDefault);

  const [enabled, setEnabledState] = useState(false);
  const [toggleError, setToggleError] = useState<string | null>(null);

  // Fetch initial state on mount (and when sessionId or global default changes).
  // When no per-session override exists, apply the global default and persist it.
  useEffect(() => {
    if (!capable || !sessionId) return;
    let cancelled = false;
    getAutoApprove(sessionId).then((res) => {
      if (cancelled) return;
      if (!res.overridden && autoApproveDefault !== res.enabled) {
        // Apply the global default and persist it so the server
        // remembers the choice for this session.
        setAutoApproveApi(sessionId, autoApproveDefault).catch(() => {});
        setEnabledState(autoApproveDefault);
      } else {
        setEnabledState(res.enabled);
      }
    }).catch(() => { /* best-effort */ });
    return () => { cancelled = true; };
  }, [sessionId, capable, getAutoApprove, setAutoApproveApi, autoApproveDefault]);

  const setEnabled = useCallback(async (on: boolean) => {
    setToggleError(null);
    try {
      await setAutoApproveApi(sessionId, on);
      setEnabledState(on);
    } catch (e) {
      setToggleError(e instanceof Error ? e.message : 'Failed to update auto-approve setting');
    }
  }, [sessionId, setAutoApproveApi]);

  return { enabled, toggleError, setEnabled };
}
