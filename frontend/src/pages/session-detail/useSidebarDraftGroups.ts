import { useMemo } from 'react';
import { useLocation } from 'react-router-dom';
import { fuzzyMatch } from '../../lib/format';
import { useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { projectIdentityIndex } from '../../lib/projectIdentity';
import type { SidebarProjectGroup } from './SessionSidebar';

export function useSidebarDraftGroups(groups: SidebarProjectGroup[], searchQuery: string) {
  const drafts = useNewConversationDrafts((state) => state.drafts);
  const location = useLocation();
  const activeId = location.pathname === '/session/new' ? new URLSearchParams(location.search).get('draftId') || 'new' : null;
  return useMemo(() => {
    const projects = groups.filter((group) => !group.isPinned);
    const identity = projectIdentityIndex(projects.flatMap((group) => [
      { ...group, projectKey: group.key },
      ...group.sessions.map((session) => ({ ...session, projectKey: group.key })),
    ]));
    const result = groups.map((group) => ({ ...group, drafts: [] as typeof drafts }));
    for (const draft of drafts) {
      const target = identity(draft.directory, draft.remoteId);
      const query = searchQuery.trim();
      if (draft.draftId !== activeId && query && !fuzzyMatch(query,
        `${draft.title || ''} ${draft.directory} ${target.directory} ${draft.remoteId || 'local'}`)) continue;
      let group = result.find((group) => !group.isPinned && (group.key ?? group.directory) === target.key);
      if (!group) {
        group = { ...target, platform: draft.platform, sessions: [], lastUpdated: draft.createdAt ?? 0,
          aggregate: { kind: 'none' }, drafts: [] };
        result.push(group);
      }
      group.drafts.push(draft);
    }
    return result;
  }, [groups, drafts, searchQuery, activeId]);
}
