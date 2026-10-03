import { useCallback } from 'react';
import { api } from '../../lib/api';
import type { PlatformCapabilities } from '../../lib/api.types';
import { newSessionPath } from '../../lib/newSessionPath';
import { projectRootForDirectory } from '../../lib/worktrees';
import { remoteLog } from '../../lib/remoteLog';
import type { SessionMetadata } from '../../lib/sessionReducer';

export interface UseSessionCreationOptions {
  session: SessionMetadata | null;
  portAvailable: boolean;
  caps: Pick<PlatformCapabilities, 'compact'>;
  selectedModel: string;
  activeModel: string;
  selectedAgent: string;
  activeAgent: string;
  setSelectedAgent: (agent: string) => void;
  navigate: (path: string) => void;
}

export interface UseSessionCreationResult {
  handleNewSessionInDirectory: (directory: string, remoteId?: string, platform?: string, title?: string) => void;
  handleNewSession: (title?: string) => Promise<void>;
  handleCompact: () => Promise<void>;
}

/**
 * Resolves the session's main checkout from the owner's worktree list, so
 * worktrees outside ocman's `.worktrees/<repo>/<slug>` layout are covered.
 * A failed lookup (e.g. not a repository) keeps the layout-based guess.
 */
async function mainCheckout(session: SessionMetadata): Promise<string> {
  try {
    const { worktrees } = await api.worktree.list(session.directory, session.remoteId || 'local');
    const main = worktrees.find((tree) => tree.main && !tree.bare)?.path;
    if (main) return main;
  } catch (e) {
    remoteLog.warn('Resolving main checkout failed; using directory layout', e);
  }
  return projectRootForDirectory(session.directory);
}

/** New-session and `/compact` actions for the open session. */
export function useSessionCreation({
  session,
  portAvailable,
  caps,
  selectedModel,
  activeModel,
  selectedAgent,
  activeAgent,
  setSelectedAgent,
  navigate,
}: UseSessionCreationOptions): UseSessionCreationResult {
  // A new conversation is a route, not a session: nothing is created until
  // its first prompt, so the machine and target can still change freely.
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
    const sameProject = !!session && (remoteId === undefined || remoteId === (session.remoteId || 'local'))
      && projectRootForDirectory(directory) === projectRootForDirectory(session.directory);
    const targetPlatform = platform ?? (sameProject ? session?.platform : undefined);
    navigate(newSessionPath({ directory, remoteId, platform: targetPlatform, title }));
  }, [navigate, session]);

  const handleNewSession = useCallback(async (title?: string) => {
    if (!session) return;
    // Start from the main checkout: a session already inside a linked worktree
    // would make the composer skip its "New worktree" target (the default).
    await handleNewSessionInDirectory(await mainCheckout(session), session.remoteId, session.platform, title);
  }, [session, handleNewSessionInDirectory]);

  const handleCompact = useCallback(async () => {
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
    }
  }, [activeAgent, activeModel, caps.compact, portAvailable, selectedAgent, selectedModel, session, setSelectedAgent]);

  return { handleNewSessionInDirectory, handleNewSession, handleCompact };
}
