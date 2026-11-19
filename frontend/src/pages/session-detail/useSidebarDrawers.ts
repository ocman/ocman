import { useState } from 'react';
import type { GitInfo, Session } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';
import { hiddenSessionOrigins } from '../../lib/sessionListFilters';
import { compareSidebarCompletion } from '../../lib/sidebarHelpers';
import { matchesSessionSearch } from './sidebarSessionSearch';
import { hasBackgroundWork, hasPendingPrompt } from '../../lib/sessionVisibility';
import { sessionAttentionKey } from '../../lib/sessionAttention';
import { nestSessions } from '../../lib/nestSessions';

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

export function runningSidebarDrawers(sessions: Session[], query: string, infos: Record<string, GitInfo>, activeId?: string, activePlatform?: string, alwaysShowPrompts = true): SidebarDrawer[] {
  const isActive = (session: Session) => session.id === activeId && (!activePlatform || session.platform === activePlatform);
  const routineKeys = hiddenSessionOrigins(sessions, { showFactory: true, showRoutines: false, showPlugins: true });
  const pluginKeys = hiddenSessionOrigins(sessions, { showFactory: true, showRoutines: true, showPlugins: false });
  const factoryKeys = hiddenSessionOrigins(sessions, { showFactory: false, showRoutines: true, showPlugins: true });
  const byKey = new Map(sessions.map(session => [`${session.platform}\0${session.id}`, session]));
  const parents = new Map(sessions.map(session => [`${session.platform}\0${session.id}`, session.parentId
    ? `${session.platform}\0${session.parentId}` : session.creator && `${session.creator.platform}\0${session.creator.sessionId}`]));
  const archivedKeys = new Set(sessions.filter(session => session.archived).map(session => `${session.platform}\0${session.id}`));
  const children = new Map<string, string[]>();
  for (const [key, parent] of parents) {
    if (!parent) continue;
    const siblings = children.get(parent) ?? [];
    siblings.push(key);
    children.set(parent, siblings);
  }
  for (const key of archivedKeys) {
    for (const child of children.get(key) ?? []) archivedKeys.add(child);
  }
  const rows: Record<SidebarDrawerId, Session[]> = { archived: [], factory: [], routines: [], plugins: [], snoozed: [] };
  const seen = new Set<string>();
  for (const session of sessions) {
    const key = `${session.platform}\0${session.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    const reveal = !session.snoozedUntil && (isActive(session) || session.pinned || alwaysShowPrompts && hasPendingPrompt(session));
    if (!reveal && (!session.snoozedUntil && session.status !== 'busy' && !hasBackgroundWork(session)
      || query && !matchesSessionSearch(query, session, infos[checkoutKey(session.directory, session.remoteId)] ?? infos[session.directory]))) continue;
    // Snoozing wins over origin and archive, so each session has one drawer.
    const category = session.snoozedUntil ? 'snoozed' : factoryKeys.has(key) ? 'factory' : routineKeys.has(key) ? 'routines' : pluginKeys.has(key) ? 'plugins' : archivedKeys.has(key) ? 'archived' : undefined;
    if (category) rows[category].push(session);
  }
  // Keep settled ancestors as context for their visible background descendants.
  const backgroundKeys = new Set(rows.archived.map(session => `${session.platform}\0${session.id}`));
  for (const key of backgroundKeys) {
    const parentKey = parents.get(key);
    const parent = parentKey && byKey.get(parentKey);
    if (!parent || !archivedKeys.has(parentKey) || backgroundKeys.has(parentKey) || parent.snoozedUntil
      || factoryKeys.has(parentKey) || routineKeys.has(parentKey) || pluginKeys.has(parentKey)) continue;
    backgroundKeys.add(parentKey);
    rows.archived.push(parent);
  }
  return DRAWERS.flatMap((drawer) => rows[drawer.id].length
    ? [{ ...drawer, sessions: rows[drawer.id].sort(compareSidebarCompletion), active: rows[drawer.id].some(isActive) }] : []);
}

export function useSidebarDrawers(sessions: Session[], query: string, infos: Record<string, GitInfo>, activeId?: string, activePlatform?: string, alwaysShowPrompts = true) {
  const drawers = runningSidebarDrawers(sessions, query, infos, activeId, activePlatform, alwaysShowPrompts);
  const activeDrawer = drawers.find(drawer => drawer.active)?.id;
  const selection = JSON.stringify([activePlatform, activeId]);
  const awaitingRow = !!activeId && !sessions.some(row => row.id === activeId && (!activePlatform || row.platform === activePlatform));
  const [state, setState] = useState(() => ({ selection, awaitingRow, expanded: new Set<SidebarDrawerId>(activeDrawer ? [activeDrawer] : []) }));
  // Reveal navigation once the selected row loads. Moving the same session into
  // a drawer after archive/snooze is not navigation and must not open it.
  if (state.selection !== selection || state.awaitingRow && !awaitingRow) setState(current => ({ selection, awaitingRow,
    expanded: activeDrawer ? new Set([...current.expanded, activeDrawer]) : current.expanded }));
  const expanded = state.expanded;
  const toggle = (id: SidebarDrawerId) => setState((current) => {
    const next = new Set(current.expanded);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return { ...current, expanded: next };
  });
  return { drawers, expanded, toggle, keys: new Set(drawers.flatMap(drawer => drawer.sessions.map(sessionAttentionKey))),
    sections: drawers.filter((drawer) => expanded.has(drawer.id)).map((drawer) =>
      nestSessions(drawer.sessions).map(({ session }) => session)) };
}
