import { useState } from 'react';
import type { GitInfo, Session } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';
import { hiddenSessionOrigins } from '../../lib/sessionListFilters';
import { compareSidebarCompletion } from '../../lib/sidebarHelpers';
import { matchesSessionSearch } from './sidebarSessionSearch';
import { hasBackgroundWork } from '../../lib/sessionVisibility';

const DRAWERS = [
  { id: 'archived', label: 'Archived', icon: 'bi-archive' },
  { id: 'factory', label: 'Factory', icon: 'bi-kanban' },
  { id: 'routines', label: 'Routines', icon: 'bi-clock' },
  { id: 'plugins', label: 'Plugins', icon: 'bi-puzzle' },
  { id: 'snoozed', label: 'Snoozed', icon: 'bi-moon' },
] as const;

export type SidebarDrawerId = typeof DRAWERS[number]['id'];
export interface SidebarDrawer {
  id: SidebarDrawerId;
  label: string;
  icon: string;
  sessions: Session[];
}

export function runningSidebarDrawers(sessions: Session[], query: string, infos: Record<string, GitInfo>, activeId?: string, activeStatus?: Session['status']): SidebarDrawer[] {
  const routineKeys = hiddenSessionOrigins(sessions, { showFactory: true, showRoutines: false, showPlugins: true });
  const pluginKeys = hiddenSessionOrigins(sessions, { showFactory: true, showRoutines: true, showPlugins: false });
  const factoryKeys = hiddenSessionOrigins(sessions, { showFactory: false, showRoutines: true, showPlugins: true });
  const rows: Record<SidebarDrawerId, Session[]> = { archived: [], factory: [], routines: [], plugins: [], snoozed: [] };
  const seen = new Set<string>();
  for (const session of sessions) {
    const key = `${session.platform}\0${session.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    const status = session.id === activeId ? activeStatus ?? session.status : session.status;
    if (!session.snoozedUntil && status !== 'busy' && !hasBackgroundWork(session)
      || query && !matchesSessionSearch(query, session, infos[checkoutKey(session.directory, session.remoteId)] ?? infos[session.directory])) continue;
    // Snoozing wins over origin and archive, so each session has one drawer.
    const category = session.snoozedUntil ? 'snoozed' : factoryKeys.has(key) ? 'factory' : routineKeys.has(key) ? 'routines' : pluginKeys.has(key) ? 'plugins' : session.archived ? 'archived' : undefined;
    if (category) rows[category].push(session);
  }
  return DRAWERS.flatMap((drawer) => rows[drawer.id].length
    ? [{ ...drawer, sessions: rows[drawer.id].sort(compareSidebarCompletion) }] : []);
}

export function useSidebarDrawers(sessions: Session[], query: string, infos: Record<string, GitInfo>, activeId?: string, activeStatus?: Session['status']) {
  const [expanded, setExpanded] = useState<ReadonlySet<SidebarDrawerId>>(() => new Set());
  const drawers = runningSidebarDrawers(sessions, query, infos, activeId, activeStatus);
  const toggle = (id: SidebarDrawerId) => setExpanded((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return next;
  });
  return { drawers, expanded, toggle, sections: drawers.filter((drawer) => expanded.has(drawer.id)).map((drawer) => drawer.sessions) };
}
