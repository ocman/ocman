import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { Session } from '../../lib/api.types';
import { useNewConversationDrafts, type ConversationDraft } from '../../lib/newConversationDrafts';
import { archiveBackendDraft, discardBackendDraft, isBackendDraft, refreshBackendDrafts, retireBackendDraft, subscribeBackendDraftSync, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { newSessionPath } from '../../lib/newSessionPath';
import { useProjectIdentityIndex } from '../../lib/useProjectIdentityIndex';
import { remoteLog } from '../../lib/remoteLog';
import { compareSidebarCompletion } from '../../lib/sidebarHelpers';
import { startVisiblePolling } from '../../lib/visiblePolling';
import type { SidebarProjectGroup } from './SessionSidebar';
import { navigateAfterDraftDiscard } from './navigateAfterDraftDiscard';

function draftSession(draft: ConversationDraft): Session {
  const backend = useBackendConversationDrafts.getState().drafts[draft.draftId];
  const request = backend?.request;
  return {
    id: draft.draftId, platform: request?.platform || draft.platform || '', projectId: '',
    directory: draft.directory, remoteId: draft.remoteId, title: draft.title || request?.prompt?.slice(0, 100) || 'New session',
    timeCreated: draft.createdAt || backend?.createdAt || 0, timeUpdated: draft.createdAt || backend?.createdAt || 0,
    summaryAdditions: null, summaryDeletions: null, summaryFiles: null, shareUrl: null,
    messageCount: 0, durationMs: 0, activeDurationMs: 0, totalInputTokens: 0, totalOutputTokens: 0, totalCost: 0,
    status: backend?.error ? 'error' : 'waiting', liveConnection: false,
    pendingPermission: false, pendingQuestion: false, archived: !!backend?.archived && !backend?.error, seen: true,
    pinned: false, pinnedAt: 0, seenTimeUpdated: 0, unreadCount: 0,
  };
}

/** Drafts and native-backed sessions use the same row and React key. */
export function useStableDraftSidebar(recent: Session[], groups: SidebarProjectGroup[], navigate: (path: string, options?: { replace: boolean }) => void, activeId?: string, showArchived = false) {
  const currentId = useRef(activeId);
  useLayoutEffect(() => { currentId.current = activeId; return () => { currentId.current = undefined; }; }, [activeId]);
  const discardInFlight = useRef(new Set<string>());
  const [discarding, setDiscarding] = useState(new Set<string>());
  const [discardError, setDiscardError] = useState<{ id: string; message: string }>();
  const drafts = useNewConversationDrafts((state) => state.drafts);
  const backend = useBackendConversationDrafts((state) => state.drafts);
  const stable = useMemo(() => drafts.filter((draft) => isBackendDraft(draft.draftId) && backend[draft.draftId]?.phase !== 'discarded'), [drafts, backend]);
  const projects = useMemo(() => groups.map((group) => ({ ...group, projectKey: group.key })), [groups]);
  const project = useProjectIdentityIndex(projects, stable);
  useEffect(() => {
    const stopSync = subscribeBackendDraftSync();
    const stopPolling = startVisiblePolling(() => refreshBackendDrafts()
      .catch((error) => remoteLog.error('Could not load backend drafts', error)), 5000);
    return () => { stopPolling(); stopSync(); };
  }, []);
  const known = useMemo(() => new Map([...recent, ...groups.flatMap((group) => group.sessions)].map((session) => [session.id, session])), [recent, groups]);
  // Once the real row has taken over, retire the background draft so a later
  // archive does not bring the synthetic row back. The open draft page does
  // this itself before navigating.
  const published = useMemo(() => stable.filter((draft) => draft.draftId !== activeId && backend[draft.draftId]?.phase === 'ready'
    && known.has(draft.draftId)).map((draft) => draft.draftId).join(','), [stable, activeId, backend, known]);
  useEffect(() => {
    for (const id of published ? published.split(',') : []) {
      void retireBackendDraft(id).catch((error) => remoteLog.error('Could not retire draft metadata', error));
    }
  }, [published]);
  // Transcript updates don't change any of these inputs. Keep row objects,
  // group arrays and action identities out of that render path.
  const derived = useMemo(() => {
    const pending = stable.filter((draft) => backend[draft.draftId]?.phase !== 'ready' || !known.has(draft.draftId));
    const ids = new Set(pending.map((draft) => draft.draftId));
    const synthetic = pending.filter((draft) => showArchived || !backend[draft.draftId]?.archived || !!backend[draft.draftId]?.error).map(draftSession);
    const merge = (rows: Session[], extras: Session[]) => {
      return [...rows.filter((row) => !ids.has(row.id)), ...extras]
        .sort(compareSidebarCompletion);
    };
    const result = groups.map((group) => ({ ...group, sessions: group.sessions.filter((session) => !ids.has(session.id)) }));
    for (const row of synthetic) {
      const identity = project(row.directory, row.remoteId);
      let group = result.find((group) => !group.isPinned && (group.key ?? group.directory) === identity.key);
      if (!group) {
        group = { ...identity, sessions: [], lastUpdated: row.timeUpdated, aggregate: { kind: 'none' } };
        result.push(group);
      }
      group.sessions = merge(group.sessions, [row, ...group.sessions.filter((session) => ids.has(session.id) && session.id !== row.id)]);
    }
    return { recent: merge(recent, synthetic), groups: result, ids };
  }, [recent, groups, stable, backend, project, known, showArchived]);
  const { ids } = derived;
  const discard = useCallback((id: string) => {
    if (!ids.has(id)) return false;
    if (discardInFlight.current.has(id)) return true;
    discardInFlight.current.add(id);
    setDiscarding(new Set(discardInFlight.current));
    setDiscardError(undefined);
    // Unsubmitted/failed allocation can be discarded. Accepted work must keep running.
    const record = useBackendConversationDrafts.getState().drafts[id];
    const archive = record && record.phase !== 'draft' && !(['failed', 'interrupted'].includes(record.phase) && record.result?.sessionId !== id);
    const afterCommit = () => {
      if (currentId.current === id) navigateAfterDraftDiscard(id, navigate);
    };
    void (archive ? archiveBackendDraft(id).then(afterCommit) : discardBackendDraft(id, afterCommit)).catch((error: unknown) => {
      setDiscardError({ id, message: `Could not ${archive ? 'archive the session' : 'discard the draft'}: ${error instanceof Error ? error.message : String(error)}` });
    }).finally(() => {
      discardInFlight.current.delete(id);
      setDiscarding(new Set(discardInFlight.current));
    });
    return true;
  }, [ids, navigate]);
  const open = useCallback((id: string) => {
    const draft = stable.find((draft) => draft.draftId === id);
    if (!draft || backend[id]?.phase === 'ready') return false;
    navigate(newSessionPath(draft));
    return true;
  }, [stable, backend, navigate]);
  const retryDiscard = useCallback(() => { if (discardError) discard(discardError.id); }, [discardError, discard]);
  return {
    ...derived, discarding,
    discardError: discardError?.message,
    retryDiscard, open, discard,
  };
}
