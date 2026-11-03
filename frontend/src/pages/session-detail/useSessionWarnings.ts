import { useState } from 'react';
import type { SessionWarning } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';

export function sessionWarningKey(sessionId: string, warning: SessionWarning): string {
  // The trailing colon keeps keys of warnings dismissed before ports were dropped.
  return `${sessionId}:${warning.kind}:${warning.message}:`;
}

/** Session warnings minus the ones the user dismissed this page lifetime. */
export function useSessionWarnings(session: SessionMetadata | null) {
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());
  const visibleSessionWarnings = session
    ? (session.warnings ?? []).filter((warning) => !dismissed.has(sessionWarningKey(session.id, warning)))
    : [];
  const dismissSessionWarning = (warning: SessionWarning) => {
    if (!session) return;
    const key = sessionWarningKey(session.id, warning);
    setDismissed((current) => {
      if (current.has(key)) return current;
      const next = new Set(current);
      next.add(key);
      return next;
    });
  };
  return { visibleSessionWarnings, dismissSessionWarning };
}
