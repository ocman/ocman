import { useCallback, useRef, useState } from 'react';
import type { FallbackRender } from '../../components/ErrorBoundary';
import { remoteLog } from '../../lib/remoteLog';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';
import { ThreadBoundaryFallback } from './ThreadBoundaryFallback';

const THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS = 5_000;

/**
 * Recovery for the thread error boundary: a known transient assistant-ui
 * error reloads the thread automatically, at most once per error and
 * session within the cooldown. Returns the boundary reset key and fallback.
 */
export function useThreadBoundaryRecovery(id: string | undefined, reload: () => Promise<void>) {
  const [threadBoundaryResetNonce, setThreadBoundaryResetNonce] = useState(0);
  const threadBoundaryRecoveryRef = useRef<{ sessionId: string | undefined; message: string; at: number } | null>(null);

  const handleThreadBoundaryRetry = useCallback((error: Error, force = false) => {
    const now = Date.now();
    const previous = threadBoundaryRecoveryRef.current;
    if (
      !force
      && previous
      && previous.sessionId === id
      && previous.message === error.message
      && now - previous.at < THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS
    ) {
      return false;
    }

    threadBoundaryRecoveryRef.current = { sessionId: id, message: error.message, at: now };
    remoteLog.warn('SessionDetail auto-recovering thread boundary', {
      sessionId: id,
      message: error.message,
    });
    setThreadBoundaryResetNonce((value) => value + 1);
    void reload();
    return true;
  }, [id, reload]);

  const renderThreadBoundaryFallback = useCallback<FallbackRender>(({ error, reset }) => {
    const previous = threadBoundaryRecoveryRef.current;
    const autoRecover = isRecoverableThreadBoundaryError(error)
      && (!previous
        || previous.sessionId !== id
        || previous.message !== error.message
        || Date.now() - previous.at >= THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS);

    return (
      <ThreadBoundaryFallback
        error={error}
        reset={reset}
        autoRecover={autoRecover}
        onReload={() => {
          handleThreadBoundaryRetry(error, !autoRecover);
        }}
      />
    );
  }, [handleThreadBoundaryRetry, id]);

  return { threadBoundaryResetNonce, renderThreadBoundaryFallback };
}
