import { useCallback, useMemo, useState } from 'react';
import type { SessionWarning } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';

export function sessionWarningKey(sessionId: string, warning: SessionWarning): string {
  return `${sessionId}:${warning.kind}:${warning.message}:${(warning.ports ?? []).join(',')}`;
}

export interface UseSessionWarningsResult {
  visibleSessionWarnings: SessionWarning[];
  dismissSessionWarning: (warning: SessionWarning) => void;
}

/** Session warnings minus the ones the user dismissed this page lifetime. */
export function useSessionWarnings(session: SessionMetadata | null): UseSessionWarningsResult {
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());
  const visibleSessionWarnings = useMemo(() => {
    if (!session) return [];
    return (session.warnings ?? []).filter((warning) => (
      !dismissed.has(sessionWarningKey(session.id, warning))
    ));
  }, [session, dismissed]);
  const dismissSessionWarning = useCallback((warning: SessionWarning) => {
    if (!session) return;
    const key = sessionWarningKey(session.id, warning);
    setDismissed((current) => {
      if (current.has(key)) return current;
      const next = new Set(current);
      next.add(key);
      return next;
    });
  }, [session]);
  return { visibleSessionWarnings, dismissSessionWarning };
}
