import { useState } from 'react';
import * as Toast from '@radix-ui/react-toast';
import { useSessionInfo } from '../lib/useSessionInfo';
import { usePlatformCapabilities } from '../lib/useCapabilities';

interface McpAuthBannerProps {
  sessionId: string;
  platformId?: string;
}

// Dismissal is persisted per *server set* (not per session) in
// localStorage: once the toast is closed — explicitly or via the 10 s
// auto-hide — the same set of servers never re-surfaces it, across
// sessions and reloads. A *different* set (e.g. a new server needing
// auth) shows it again. The signal stays visible in the Session Info
// sidebar regardless.
const DISMISS_STORAGE_KEY = 'ocman.mcp-auth-dismissed';

// localStorage access follows the repo convention (composerDraft.ts):
// wrapped in try/catch so a missing/blocked Storage (private mode,
// jsdom) degrades to session-only dismissal instead of throwing.
function loadDismissed(): string | null {
  try {
    return window.localStorage.getItem(DISMISS_STORAGE_KEY);
  } catch {
    return null;
  }
}

function saveDismissed(key: string) {
  try {
    window.localStorage.setItem(DISMISS_STORAGE_KEY, key);
  } catch {
    // best-effort
  }
}

// McpAuthBanner surfaces MCP servers that need authentication as an
// auto-hiding toast in the shared toast viewport — the same signal
// already shown passively in the Session Info sidebar. Renders
// nothing when no server has status "needs_auth".
export function McpAuthBanner({ sessionId, platformId }: McpAuthBannerProps) {
  const caps = usePlatformCapabilities(platformId);
  const { data } = useSessionInfo(sessionId, { enabled: caps.sessionInfo });
  const [dismissedKey, setDismissedKey] = useState<string | null>(loadDismissed);

  const needsAuth = (data?.mcpServers ?? []).filter((s) => s.status === 'needs_auth');
  if (needsAuth.length === 0) {
    return null;
  }
  const key = needsAuth.map((s) => s.name).sort().join(',');
  const dismiss = () => {
    saveDismissed(key);
    setDismissedKey(key);
  };

  return (
    <Toast.Root
      key={key}
      className="oc-toast-root warning"
      open={dismissedKey !== key}
      onOpenChange={(open) => { if (!open) dismiss(); }}
      duration={10000}
    >
      <Toast.Description className="oc-toast-description" data-testid="mcp-auth-banner">
        <i className="bi bi-exclamation-triangle" aria-hidden="true" />
        {' '}
        <strong>MCP authentication required</strong>
        {' — '}
        {needsAuth.map((s, i) => (
          <span key={s.name}>
            {i > 0 && ', '}
            {s.name}
            {s.authHint && (
              <>
                {' '}
                (run <code>{s.authHint}</code>)
              </>
            )}
          </span>
        ))}
      </Toast.Description>
    </Toast.Root>
  );
}
