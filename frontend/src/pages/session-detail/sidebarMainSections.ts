import type { GitInfo, Session } from '../../lib/api';
import { fuzzyMatch } from '../../lib/format';
import { nestSessions } from '../../lib/nestSessions';
import { checkoutKey } from '../../lib/projectIdentity';
import { sessionAttentionKey } from '../../lib/sessionAttention';
import { matchesSessionListFilters, type SessionListFilters } from '../../lib/sessionListFilters';
import type { SidebarProjectGroup } from './SessionSidebar';
import { matchesSessionSearch } from './sidebarSessionSearch';

export function sidebarMainSections({ view, recent, groups, query, gitInfos, filters, hidden, drawerKeys, activeId, activePlatform }: {
  view: 'recent' | 'projects';
  recent: Session[];
  groups: SidebarProjectGroup[];
  query: string;
  gitInfos: Record<string, GitInfo>;
  filters: SessionListFilters;
  hidden: ReadonlySet<string>;
  drawerKeys: ReadonlySet<string>;
  activeId?: string;
  activePlatform?: string;
}) {
  const matches = (session: Session, projectMatches = false) =>
    !session.pinned && !drawerKeys.has(sessionAttentionKey(session)) &&
    matchesSessionListFilters(session, filters, hidden,
      !query || projectMatches || matchesSessionSearch(query, session, gitInfos[checkoutKey(session.directory, session.remoteId)] ?? gitInfos[session.directory]),
      activePlatform && session.platform !== activePlatform ? undefined : activeId);
  const pins = recent.filter((session) => session.pinned && !session.snoozedUntil && !drawerKeys.has(sessionAttentionKey(session)))
    .sort((a, b) => b.pinnedAt - a.pinnedAt);
  const flat = view === 'recent' ? recent.filter((session) => matches(session)) : [];
  const projects = view === 'projects' ? groups.filter((group) => !group.isPinned).map((group) => {
    const projectMatches = !!query && fuzzyMatch(query, group.directory);
    return { group, projectMatches, sessions: group.sessions.filter((session) => matches(session, projectMatches)) };
  }) : [];

  // Move visible descendants with their pinned ancestor, even across projects.
  const candidates = pins.length ? [...pins, ...flat, ...projects.flatMap(({ sessions }) => sessions)] : [];
  const pinnedKeys = new Set<string>();
  let pinnedDepth: number | undefined;
  for (const { session, depth } of nestSessions(candidates)) {
    if (pinnedDepth !== undefined && depth <= pinnedDepth) pinnedDepth = undefined;
    if (session.pinned) pinnedDepth ??= depth;
    if (pinnedDepth !== undefined) pinnedKeys.add(sessionAttentionKey(session));
  }
  const isInPinned = (session: Session) => pinnedKeys.has(sessionAttentionKey(session));
  return {
    pinned: candidates.filter(isInPinned),
    flat: flat.filter((session) => !isInPinned(session)),
    projects: projects.flatMap(({ group, projectMatches, sessions }) => {
      const remaining = sessions.filter((session) => !isInPinned(session));
      return query && !projectMatches && remaining.length === 0 && !group.drafts?.length
        ? [] : [{ ...group, sessions: remaining }];
    }),
  };
}
