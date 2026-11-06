import { useEffect, useState, type ReactNode } from 'react';
import { refreshBackendDrafts, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { newSessionPath, type NewSessionParams } from '../../lib/newSessionPath';
import { Button } from '../../components/Control';

/** Recover server text and selections before any composer can register defaults. */
export function DurableDraftRecovery({ children, params }: { children: ReactNode; params: NewSessionParams }) {
  const id = params.draftId ?? '';
  const target = useBackendConversationDrafts((state) => state.recoveryTargets[id]);
  const routeMatches = !target || newSessionPath(params) === newSessionPath(target);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!target || !routeMatches) return;
    useBackendConversationDrafts.setState((state) => {
      if (state.recoveryTargets[id] !== target) return state;
      const recoveryTargets = { ...state.recoveryTargets };
      delete recoveryTargets[id];
      return { recoveryTargets };
    });
  }, [id, target, routeMatches]);
  useEffect(() => {
    let active = true;
    void refreshBackendDrafts().then(() => { if (active) setReady(true); }).catch((cause) => {
      if (active) setError(cause instanceof Error ? cause.message : String(cause));
    });
    return () => { active = false; };
  }, [attempt]);
  if (ready && routeMatches) return children;
  return <div role="status">{error || 'Recovering saved conversation…'}{error && <Button onClick={() => { setError(''); setAttempt(attempt + 1); }}>Retry</Button>}</div>;
}
