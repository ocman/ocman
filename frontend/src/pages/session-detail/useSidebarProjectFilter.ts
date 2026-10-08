import { useMemo, useState } from 'react';
import type { Session } from '../../lib/api';
import type { SidebarProjectGroup } from './SessionSidebar';

export function useSidebarProjectFilter(sessions: Session[], groups: SidebarProjectGroup[]) {
  const [projectFilter, setProjectFilter] = useState('');
  const projects = useMemo(() => groups.filter((group) => !group.isPinned), [groups]);
  const filtered = useMemo(() => {
    if (!projectFilter) return { recentSessions: sessions, sidebarProjectGroups: groups };
    const selected = projects.find((group) => (group.key ?? group.directory) === projectFilter);
    const members = new Set(selected?.sessions.map((session) => `${session.platform}\0${session.id}`));
    const recentSessions = sessions.filter((session) => members.has(`${session.platform}\0${session.id}`));
    const sidebarProjectGroups = selected ? [selected] : [];
    return { recentSessions, sidebarProjectGroups };
  }, [sessions, groups, projects, projectFilter]);
  return { ...filtered, projects, projectFilter, setProjectFilter };
}
