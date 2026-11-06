import { useState } from 'react';
import type { GitInfo, Session } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';
import { hiddenSessionOrigins } from '../../lib/sessionListFilters';
import { compareSidebarCompletion } from '../../lib/sidebarHelpers';
import { matchesSessionSearch } from './sidebarSessionSearch';
import { hasBackgroundWork, hasPendingPrompt } from '../../lib/sessionVisibility';
import { sessionAttentionKey } from '../../lib/sessionAttention';

const DRAWERS = [
  { id: 'archived', label: 'Background', icon: 'bi-archive' },
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
  active: boolean;
}

export function runningSidebarDrawers(sessions: Session[], query: string, infos: Record<string, GitInfo>, activeId?: string, activeStatus?: Session['status'], alwaysShowPrompts = true): SidebarDrawer[] {
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
    const reveal = !session.snoozedUntil && (session.id === activeId || session.pinned || alwaysShowPrompts && hasPendingPrompt(session));
    if (!reveal && (!session.snoozedUntil && status !== 'busy' && !hasBackgroundWork(session)
      || query && !matchesSessionSearch(query, session, infos[checkoutKey(session.directory, session.remoteId)] ?? infos[session.directory]))) continue;
    // Snoozing wins over origin and archive, so each session has one drawer.
    const category = session.snoozedUntil ? 'snoozed' : factoryKeys.has(key) ? 'factory' : routineKeys.has(key) ? 'routines' : pluginKeys.has(key) ? 'plugins' : session.archived ? 'archived' : undefined;
    if (category) rows[category].push(session);
  }
  return DRAWERS.flatMap((drawer) => rows[drawer.id].length
    ? [{ ...drawer, sessions: rows[drawer.id].sort(compareSidebarCompletion), active: rows[drawer.id].some(session => session.id === activeId) }] : []);
}

export function useSidebarDrawers(sessions: Session[], query: string, infos: Record<string, GitInfo>, activeId?: string, activeStatus?: Session['status'], alwaysShowPrompts = true) {
  const drawers = runningSidebarDrawers(sessions, query, infos, activeId, activeStatus, alwaysShowPrompts);
  const activeDrawer = drawers.find(drawer => drawer.active)?.id;
  const selection = JSON.stringify([activeId, activeDrawer]);
  const [state, setState] = useState(() => ({ selection, expanded: new Set<SidebarDrawerId>(activeDrawer ? [activeDrawer] : []) }));
  // Reveal navigation changes once, without reopening manually collapsed drawers
  // on status updates or adding a post-render effect that moves the selection.
  if (state.selection !== selection) setState(current => ({ selection,
    expanded: activeDrawer ? new Set([...current.expanded, activeDrawer]) : current.expanded }));
  const expanded = state.expanded;
  const toggle = (id: SidebarDrawerId) => setState((current) => {
    const next = new Set(current.expanded);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return { ...current, expanded: next };
  });
  return { drawers, expanded, toggle, keys: new Set(drawers.flatMap(drawer => drawer.sessions.map(sessionAttentionKey))),
    sections: drawers.filter((drawer) => expanded.has(drawer.id)).map((drawer) => drawer.sessions) };
}
