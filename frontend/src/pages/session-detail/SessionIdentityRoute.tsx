import { useEffect, useState, type ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { LoadingState } from '../../components/LoadingState';
import { InlineAlert } from '../../components/InlineAlert';
import { useCapabilities } from '../../lib/useCapabilities';
import { resolveLegacySession } from '../../lib/sessionIdentity';
import { ensureSessionBrowserState, migrateSessionBrowserState } from '../../lib/sessionIdentityState';

// Resolve legacy links before opening a stream or loading native-keyed caches.
// Normal Ocman-ID navigation keeps the inner session component mounted.
export function SessionIdentityRoute({ id, children }: { id?: string; children: ReactNode }) {
  const capabilities = useCapabilities();
  const location = useLocation();
  const navigate = useNavigate();
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null);
  const [attempt, setAttempt] = useState(0);
  const key = `${id ?? ''}${location.search}`;
  const legacy = !!id && id !== 'new' && !id.startsWith('ocm_');

  useEffect(() => {
    if (!capabilities?.ownedSessionIds || !id || id === 'new') return;
    const controller = new AbortController();
    const query = new URLSearchParams(location.search);
    const resolution = legacy
      ? resolveLegacySession(id, query.get('platform') ?? undefined, controller.signal).then(async (identity) => {
        if (controller.signal.aborted) return;
        await migrateSessionBrowserState(identity);
        if (controller.signal.aborted) return;
        query.set('platform', identity.platform);
        navigate(`/session/${encodeURIComponent(identity.id)}?${query}${location.hash}`, { replace: true });
      })
      : ensureSessionBrowserState(id);
    void resolution.catch((error: unknown) => {
      if (!controller.signal.aborted) setFailure({ key, message: error instanceof Error ? error.message : 'Session identity is unavailable' });
    });
    return () => controller.abort();
  }, [id, legacy, capabilities?.ownedSessionIds, key, location.search, location.hash, navigate, attempt]);

  if (!legacy || (capabilities && !capabilities.ownedSessionIds)) return <>{failure?.key === key && <InlineAlert onRetry={() => { setFailure(null); setAttempt((n) => n + 1); }}>{failure.message}</InlineAlert>}{children}</>;
  if (failure?.key === key) return <InlineAlert onRetry={() => { setFailure(null); setAttempt((n) => n + 1); }}>{failure.message}</InlineAlert>;
  return <LoadingState>Resolving session…</LoadingState>;
}
