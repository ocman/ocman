import { useCallback, useEffect, useRef, useState } from 'react';
import { useApiStore } from './apiStore';
import { useUiStore } from './uiStore';
import type { PendingPermission } from './sseHelpers';
import type { SessionAction } from './sessionReducer';

export interface UseAutoApproveOptions {
  sessionId: string;
  /** Whether the platform supports auto-approve at all. */
  capable: boolean;
  /** Reducer dispatch so approved permissions are recorded in the thread. */
  dispatch: (action: SessionAction) => void;
}

export interface UseAutoApproveResult {
  /** Whether auto-approve is currently enabled for this session. */
  enabled: boolean;
  /** True while the judge is running for the current permission. */
  checking: boolean;
  /**
   * The OpenCode session ID that hosted the most recent judge run.
   * Non-empty after the judge has started, even while still checking.
   * Cleared when a new permission arrives. Use to link the user to
   * the reasoning session.
   */
  judgeSessionId: string | null;
  /** Error message from enabling/disabling, if any. */
  toggleError: string | null;
  /** Enable or disable auto-approve for this session. */
  setEnabled: (on: boolean) => Promise<void>;
  /**
   * Run the judge for a pending permission. If the verdict is safe,
   * calls respondPermission directly via the store (stable reference,
   * not a stale closure). Calls onApproved so the caller can clear
   * the prompt from local state.
   */
  runJudge: (
    permission: PendingPermission,
    onApproved: (permissionId: string) => void,
  ) => void;
  /**
   * Cancel any in-progress judge for the given permission ID.
   * Call this when the human responds manually so the delayed judge
   * doesn't fire after the window expires.
   */
  cancelJudge: (permissionId: string) => void;
}

/**
 * Manages per-session auto-approve state and runs the LLM judge
 * against incoming permission requests.
 *
 * The hook fetches the current enabled/disabled state from the server
 * on mount. When enabled and a pending permission is present, callers
 * should call runJudge() to start the judge flow.
 */
export function useAutoApprove({
  sessionId,
  capable,
  dispatch,
}: UseAutoApproveOptions): UseAutoApproveResult {
  const getAutoApprove = useApiStore((s) => s.getAutoApprove);
  const setAutoApproveApi = useApiStore((s) => s.setAutoApprove);
  const judgePermission = useApiStore((s) => s.judgePermission);
  const respondPermission = useApiStore((s) => s.respondPermission);

  // Global defaults from settings.
  const autoApproveDefault = useUiStore((s) => s.autoApproveDefault);
  const autoApproveDelayMs = useUiStore((s) => s.autoApproveDelayMs);

  const [enabled, setEnabledState] = useState(false);
  const [checking, setChecking] = useState(false);
  const [judgeSessionId, setJudgeSessionId] = useState<string | null>(null);
  const [toggleError, setToggleError] = useState<string | null>(null);

  // Keep stable refs so judge callbacks see the latest values.
  const enabledRef = useRef(enabled);
  const delayRef = useRef(autoApproveDelayMs);
  useEffect(() => { enabledRef.current = enabled; }, [enabled]);
  useEffect(() => { delayRef.current = autoApproveDelayMs; }, [autoApproveDelayMs]);

  // Track the permission ID currently being judged to avoid firing the
  // judge twice for the same permission (e.g. on rapid re-renders).
  const judgingIdRef = useRef<string | null>(null);

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

  const runJudge = useCallback((
    permission: PendingPermission,
    onApproved: (permissionId: string) => void,
  ) => {
    if (!enabledRef.current || !capable) return;
    // Only one judge at a time per permission ID.
    if (judgingIdRef.current === permission.permissionId) return;
    judgingIdRef.current = permission.permissionId;
    setJudgeSessionId(null); // clear previous before new run

    // Capture at call time — these won't change over the judge's lifetime.
    const targetSessionId = permission.sessionId || sessionId;
    const permissionId = permission.permissionId;
    const delay = delayRef.current;

    void (async () => {
      // Human-review window: wait before starting the judge so the
      // human has a chance to respond manually. If they do,
      // `judgingIdRef` will be null and we bail out silently.
      if (delay > 0) {
        await new Promise<void>((resolve) => setTimeout(resolve, delay));
      }
      if (judgingIdRef.current !== permissionId) return;

      setChecking(true);
      try {
        const result = await judgePermission(
          targetSessionId,
          permissionId,
          permission.permission,
          permission.patterns,
        );
        if (result.judgeSessionId) {
          setJudgeSessionId(result.judgeSessionId);
        }
        if (result.verdict === 'safe') {
          await respondPermission(targetSessionId, permissionId, 'once');
          dispatch({
            type: 'addNotice',
            notice: {
              permission: permission.permission,
              patterns: permission.patterns,
              judgeSessionId: result.judgeSessionId,
              approvedAt: Date.now(),
            },
          });
          onApproved(permissionId);
        }
      } catch {
        // Judge error or respondPermission failure → fall through to human.
      } finally {
        setChecking(false);
        if (judgingIdRef.current === permissionId) {
          judgingIdRef.current = null;
        }
      }
    })();
  }, [capable, sessionId, judgePermission, respondPermission, dispatch]);

  const cancelJudge = useCallback((permissionId: string) => {
    if (judgingIdRef.current === permissionId) {
      judgingIdRef.current = null;
      setChecking(false);
    }
  }, []);

  return { enabled, checking, judgeSessionId, toggleError, setEnabled, runJudge, cancelJudge };
}
