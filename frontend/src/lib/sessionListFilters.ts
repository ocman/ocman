import type { Session } from './api';
import { hasPendingPrompt } from './sessionVisibility';

export const SESSION_FILTER_DEFAULTS = {
  alwaysShowPrompts: true,
  showArchived: false,
  showSnoozed: false,
  showChildren: true,
  showFactory: false,
  showRoutines: false,
  showPlugins: false,
};
export type SessionListFilters = typeof SESSION_FILTER_DEFAULTS;
export type SessionOriginFilters = Pick<SessionListFilters, 'showFactory' | 'showRoutines' | 'showPlugins'>;

export const SESSION_FILTER_FIELDS = [
  ['alwaysShowPrompts', 'Always show prompts'],
  ['showArchived', 'Show archived'],
  ['showSnoozed', 'Show snoozed'],
  ['showChildren', 'Show children'],
  ['showFactory', 'Show factory'],
  ['showRoutines', 'Show routines'],
  ['showPlugins', 'Show plugins'],
] as const;

export function hiddenSessionOrigins(sessions: readonly Session[], { showFactory, showRoutines, showPlugins }: SessionOriginFilters): Set<string> {
  const hidden = new Set<string>();
  const children = new Map<string, string[]>();
  for (const session of sessions) {
    const key = `${session.platform}\0${session.id}`;
    if ((!showFactory && session.factoryAttemptId) || (!showRoutines && session.routineId)
      || (!showPlugins && session.origin === 'plugin')) hidden.add(key);
    if (!session.parentId) continue;
    const parent = `${session.platform}\0${session.parentId}`;
    const siblings = children.get(parent) ?? [];
    siblings.push(key);
    children.set(parent, siblings);
  }
  // Set iteration includes newly-added descendants; membership also stops cycles.
  for (const key of hidden) {
    for (const child of children.get(key) ?? []) hidden.add(child);
  }
  return hidden;
}

/** Shared sidebar/phone rules. Snoozing wins even over pins and pending prompts. */
export function matchesSessionListFilters(session: Session, filters: SessionListFilters, hidden: ReadonlySet<string>, matchesSearch = true, activeId?: string): boolean {
  if (session.snoozedUntil && !filters.showSnoozed) return false;
  if (session.id === activeId || session.pinned || filters.alwaysShowPrompts && hasPendingPrompt(session)) return true;
  return !hidden.has(`${session.platform}\0${session.id}`)
    && (session.snoozedUntil ? filters.showSnoozed : filters.showArchived || !session.archived)
    && (filters.showChildren || !session.parentId)
    && matchesSearch;
}
