import { useEffect } from 'react';
import type { Session } from '../../lib/api.types';
import { useNewConversationDrafts, type ConversationDraft } from '../../lib/newConversationDrafts';
import { discardBackendDraft, isBackendDraft, refreshBackendDrafts, subscribeBackendDraftSync, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { newSessionPath } from '../../lib/newSessionPath';
import { useProjectIdentityIndex } from '../../lib/useProjectIdentityIndex';
import { remoteLog } from '../../lib/remoteLog';
import type { SidebarProjectGroup } from './SessionSidebar';

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
    pendingPermission: false, pendingQuestion: false, archived: false, seen: true,
    pinned: false, pinnedAt: 0, seenTimeUpdated: 0, unreadCount: 0,
  };
}

/** Drafts and native-backed sessions use the same row and React key. */
export function useStableDraftSidebar(recent: Session[], groups: SidebarProjectGroup[], navigate: (path: string) => void) {
  const drafts = useNewConversationDrafts((state) => state.drafts);
  const backend = useBackendConversationDrafts((state) => state.drafts);
  const stable = drafts.filter((draft) => isBackendDraft(draft.draftId) && backend[draft.draftId]?.phase !== 'discarded');
  const project = useProjectIdentityIndex(groups.map((group) => ({ ...group, projectKey: group.key })), stable);
  useEffect(() => {
    const stopSync = subscribeBackendDraftSync();
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = () => {
      void refreshBackendDrafts().catch((error) => remoteLog.error('Could not load backend drafts', error)).finally(() => {
        if (active) timer = setTimeout(refresh, 5000);
      });
    };
    refresh();
    return () => { active = false; clearTimeout(timer); stopSync(); };
  }, []);
  const known = new Map([...recent, ...groups.flatMap((group) => group.sessions)].map((session) => [session.id, session]));
  const pending = stable.filter((draft) => backend[draft.draftId]?.phase !== 'ready' || !known.has(draft.draftId));
  const ids = new Set(pending.map((draft) => draft.draftId));
  const synthetic = pending.map(draftSession);
  const merge = (rows: Session[], extras: Session[]) => {
    return [...rows.filter((row) => !ids.has(row.id)), ...extras]
      .sort((a, b) => b.timeUpdated - a.timeUpdated || a.id.localeCompare(b.id));
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
  return {
    recent: merge(recent, synthetic), groups: result, ids,
    open(id: string) {
      const draft = stable.find((draft) => draft.draftId === id);
      if (!draft || backend[id]?.phase === 'ready') return false;
      navigate(newSessionPath(draft));
      return true;
    },
    discard(id: string) {
      if (!ids.has(id)) return false;
      if (backend[id] && backend[id].phase !== 'draft') return true;
      void discardBackendDraft(id).catch((error) => remoteLog.error('Could not discard backend draft', error));
      return true;
    },
  };
}
