import { useEffect, useState, type ReactNode } from 'react';
import { refreshBackendDrafts } from '../../lib/backendConversationDrafts';
import { Button } from '../../components/Control';

/** Recover server text and selections before any composer can register defaults. */
export function DurableDraftRecovery({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    void refreshBackendDrafts().then(() => { if (active) setReady(true); }).catch((cause) => {
      if (active) setError(cause instanceof Error ? cause.message : String(cause));
    });
    return () => { active = false; };
  }, [attempt]);
  if (ready) return children;
  return <div role="status">{error || 'Recovering saved conversation…'}{error && <Button onClick={() => { setError(''); setAttempt(attempt + 1); }}>Retry</Button>}</div>;
}
