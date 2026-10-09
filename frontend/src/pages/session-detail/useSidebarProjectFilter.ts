import { useMemo, useState } from 'react';
import type { Session } from '../../lib/api';
import type { SidebarProjectGroup } from './SessionSidebar';
import { matchesScope } from '../../lib/projectTree';
import { hasPendingPrompt } from '../../lib/sessionVisibility';

export function useSidebarProjectFilter(sessions: Session[], groups: SidebarProjectGroup[], alwaysShowPrompts = false) {
  const [projectFilter, setProjectFilter] = useState('');
  const projects = useMemo(() => groups.filter((group) => !group.isPinned), [groups]);
  const filtered = useMemo(() => {
    if (!projectFilter) return { recentSessions: sessions, sidebarProjectGroups: groups };
    const scope = projectFilter.startsWith('scope:') ? projectFilter.slice(6) : null;
    const selected = scope === null ? projects.find((group) => (group.key ?? group.directory) === projectFilter) : undefined;
    const selectedGroups = selected ? [selected] : projects.filter((group) => matchesScope(group.directory, scope ?? projectFilter));
    const sidebarProjectGroups = groups.flatMap((group) => selectedGroups.includes(group) ? [group]
      : alwaysShowPrompts && group.sessions.some(hasPendingPrompt) ? [{ ...group, sessions: group.sessions.filter(hasPendingPrompt), drafts: [] }] : []);
    const members = new Set(sidebarProjectGroups.flatMap((group) => group.sessions.map((session) => `${session.platform}\0${session.id}`)));
    const recentSessions = sessions.filter((session) => members.has(`${session.platform}\0${session.id}`) || alwaysShowPrompts && hasPendingPrompt(session));
    return { recentSessions, sidebarProjectGroups };
  }, [sessions, groups, projects, projectFilter, alwaysShowPrompts]);
  return { ...filtered, projects, projectFilter, setProjectFilter };
}
