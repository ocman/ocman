import { api } from '../../lib/api';
import type { PlatformCapabilities } from '../../lib/api.types';
import { newSessionPath } from '../../lib/newSessionPath';
import { projectRootForDirectory } from '../../lib/worktrees';
import { remoteLog } from '../../lib/remoteLog';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { useCallback, useEffect, useRef } from 'react';
import type { NewSessionParams } from '../../lib/newSessionPath';

export interface UseSessionCreationOptions {
  session: SessionMetadata | null;
  newConversation?: NewSessionParams | null;
  portAvailable: boolean;
  caps: Pick<PlatformCapabilities, 'compact'>;
  selectedModel: string;
  activeModel: string;
  selectedAgent: string;
  activeAgent: string;
  setSelectedAgent: (agent: string) => void;
  navigate: (path: string) => void;
}

/**
 * Resolves the session's main checkout from the owner's worktree list, so
 * worktrees outside ocman's `.worktrees/<repo>/<slug>` layout are covered.
 * Without an owner-verified main checkout, retain the requested directory.
 */
async function mainCheckout(directory: string, remoteId: string | undefined, signal: AbortSignal): Promise<string> {
  try {
    const { worktrees } = await api.worktree.list(directory, remoteId || 'local', signal);
    const main = worktrees.find((tree) => tree.main && !tree.bare)?.path;
    if (main) return main;
  } catch (e) {
    if (!signal.aborted) remoteLog.warn('Resolving main checkout failed; keeping requested directory', e);
  }
  return directory;
}

/** New-session and `/compact` actions for the open session. */
export function useSessionCreation({
  session,
  newConversation,
  portAvailable,
  caps,
  selectedModel,
  activeModel,
  selectedAgent,
  activeAgent,
  setSelectedAgent,
  navigate,
}: UseSessionCreationOptions) {
  // A new conversation is a route, not a session: nothing is created until
  // its first prompt, so the machine and target can still change freely.
  const hasSession = !!session;
  const sessionDirectory = session?.directory ?? newConversation?.directory;
  const sessionRemoteId = session?.remoteId ?? newConversation?.remoteId;
  const sessionPlatform = session?.platform ?? newConversation?.platform;
  const checkout = useRef<{ controller: AbortController; directory: Promise<string> } | undefined>(undefined);
  useEffect(() => {
    if (!sessionDirectory) return;
    const controller = new AbortController();
    // Resolve while viewing the session, not on the shortcut's critical path.
    // Reuse the promise for early presses as well as the settled result. Streaming
    // metadata and same-checkout session switches must not restart this read.
    checkout.current = { controller, directory: mainCheckout(sessionDirectory, sessionRemoteId, controller.signal) };
    return () => {
      controller.abort();
      checkout.current = undefined;
    };
  }, [sessionDirectory, sessionRemoteId]);

  const handleNewSessionInDirectory = useCallback((directory: string, remoteId?: string, platform?: string, title?: string) => {
    // Prefer the target project's own platform/host (e.g. a remote
    // project group) over the currently-open session's, so a "+" on a
    // remote project actually targets that remote instead of falling
    // back to the local adapter.
    //
    // Only inherit the open session's platform when the target is the
    // same project — otherwise a "+" on a *different* project (whose
    // group didn't carry a platform) leaks the current session's
    // (possibly remote) platform onto it, mis-targeting the host.
    const sameProject = hasSession && (remoteId === undefined || remoteId === (sessionRemoteId || 'local'))
      && projectRootForDirectory(directory) === projectRootForDirectory(sessionDirectory ?? '');
    const targetPlatform = platform ?? (sameProject ? sessionPlatform : undefined);
    navigate(newSessionPath({ directory, remoteId, platform: targetPlatform, title }));
  }, [hasSession, sessionDirectory, sessionRemoteId, sessionPlatform, navigate]);

  const handleNewSession = async (title?: string) => {
    const resolved = checkout.current;
    if (!sessionDirectory || !resolved) return;
    // Start from the main checkout: a session already inside a linked worktree
    // would make the composer skip its "New worktree" target (the default).
    const directory = await resolved.directory;
    if (resolved.controller.signal.aborted) return;
    handleNewSessionInDirectory(directory, sessionRemoteId, sessionPlatform, title);
  };

  const handleCompact = async () => {
    if (!session || !portAvailable || !caps.compact) return;
    const model = selectedModel || activeModel || '';
    const slashIdx = model.indexOf('/');
    const providerID = slashIdx > 0 ? model.slice(0, slashIdx) : '';
    const modelID = slashIdx > 0 ? model.slice(slashIdx + 1) : model;
    const agentBeforeCompact = selectedAgent || activeAgent || '';
    try {
      await api.compactSession(session.id, providerID, modelID);
      if (agentBeforeCompact) setSelectedAgent(agentBeforeCompact);
    } catch (e) {
      remoteLog.error('Failed to compact session', e);
      throw e;
    }
  };

  return { handleNewSessionInDirectory, handleNewSession, handleCompact };
}
