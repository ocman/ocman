import { useState } from 'react';
import { cleanTitle, fuzzyMatch } from '../../lib/format';
import { usePageTitle } from '../../lib/headerContext';
import { SessionTable } from '../../components/SessionTable';
import { TimeRangeControl } from '../../components/TimeRangeControl';
import { InlineAlert } from '../../components/InlineAlert';
import { matchesScope } from '../../lib/projectTree';
import { useUiStore } from '../../lib/uiStore';
import { useDashboard as useDashboardCtx } from './context';
import { DashboardToolbar } from './DashboardToolbar';
import { SessionRangeFilters } from '../../components/SessionRangeFilters';
import { useIsPhone } from '../../lib/useIsPhone';
import styles from './SessionsTab.module.css';
import { PhoneSessionsToolbar } from './PhoneSessionsToolbar';
import { SessionFilterControls } from '../../components/SessionFilterControls';
import { useSessionListFilters } from '../../lib/useSessionListFilters';
import { useSessionFilterPreference } from '../../lib/useSessionFilterPreference';
import { SESSION_FILTER_DEFAULTS, SESSION_FILTER_FIELDS, hiddenSessionOrigins, matchesSessionListFilters } from '../../lib/sessionListFilters';
import { hasPendingPrompt } from '../../lib/sessionVisibility';
import { useProjectIdentityIndex } from '../../lib/useProjectIdentityIndex';
import { useSessionAccounting } from '../../lib/useSessionAccounting';

// ---------------------------------------------------------------------------
// Sessions tab
// ---------------------------------------------------------------------------

export function SessionsTab() {
  usePageTitle('Sessions');
  const { sessions: compactSessions, projects, sessionsLoading, sessionsError, loadSessions, timeRange, setTimeRange, showArchived, setShowArchived, dirScope, setDirScope } = useDashboardCtx();
  const openProjectSessionPalette = useUiStore((s) => s.openProjectSessionPalette);
  const [search, setSearch] = useState('');
  const { sessions, pendingKeys: activityPendingKeys, ...activityQ } = useSessionAccounting(compactSessions, { sinceHours: timeRange > 0 ? timeRange : undefined });
  const phone = useIsPhone();
  const { alwaysShowPrompts, setAlwaysShowPrompts, showChildren, setShowChildren, showFactory, setShowFactory,
    showRoutines, setShowRoutines, showPlugins, setShowPlugins, showSnoozed, setShowSnoozed } = useSessionListFilters();
  const [archivedPreference, setArchivedPreference] = useSessionFilterPreference('archived', SESSION_FILTER_DEFAULTS.showArchived);
  const filters = { alwaysShowPrompts, showArchived: showArchived || archivedPreference, showSnoozed, showChildren, showFactory, showRoutines, showPlugins };
  const hidden = hiddenSessionOrigins(sessions, filters);

  const filterControls = <>
    <SessionRangeFilters label="Session filters" className={styles.filters} timeRange={timeRange} onTimeRangeChange={setTimeRange}
      showArchived={showArchived} onShowArchivedChange={setShowArchived} />
  </>;

  const q = search.trim();
  const identity = useProjectIdentityIndex(projects, sessions, !!dirScope);
  const filteredSessions = sessions
    .filter((s) => matchesScope(identity(s.directory, s.remoteId).directory, dirScope) || phone && alwaysShowPrompts && hasPendingPrompt(s))
    .filter((s) => {
      const searchMatches = !q || fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`);
      return phone ? matchesSessionListFilters(s, filters, hidden, searchMatches) : searchMatches;
    });

  return (
    <>
      {sessionsError && (
        <InlineAlert onRetry={() => loadSessions()} retrying={sessionsLoading}>
          {sessionsError}
        </InlineAlert>
      )}
      {activityQ.error && (
        <InlineAlert onRetry={activityQ.retry} retrying={activityQ.retrying}>
          Could not load session activity: {activityQ.error}
        </InlineAlert>
      )}
      {phone ? <PhoneSessionsToolbar projects={projects} dirScope={dirScope} setDirScope={setDirScope}
        search={search} setSearch={setSearch} onNewSession={() => openProjectSessionPalette()}
        filtersActive={SESSION_FILTER_FIELDS.some(([key]) => filters[key] !== SESSION_FILTER_DEFAULTS[key])}>
        <TimeRangeControl value={timeRange} onChange={setTimeRange} />
        <SessionFilterControls value={filters} onChange={(key, checked) => ({
          alwaysShowPrompts: setAlwaysShowPrompts,
          showArchived: (value: boolean) => { setArchivedPreference(value); setShowArchived(value); },
          showSnoozed: setShowSnoozed, showChildren: setShowChildren,
          showFactory: setShowFactory, showRoutines: setShowRoutines, showPlugins: setShowPlugins,
        })[key](checked)} />
      </PhoneSessionsToolbar> : <><DashboardToolbar
        projects={projects}
        dirScope={dirScope}
        setDirScope={setDirScope}
        search={search}
        setSearch={setSearch}
        searchLabel="Search sessions"
        actionIcon="bi-plus-lg"
        actionLabel="New session"
        actionTitle="Create a new OpenCode session in a known project"
        onAction={() => openProjectSessionPalette()}
      />
        {filterControls}</>}
      <SessionTable
        sessions={filteredSessions}
        showProject
        loading={sessionsLoading && sessions.length === 0}
        includeArchived={phone ? filters.showArchived : showArchived}
        prefiltered={phone}
        activityPendingKeys={activityPendingKeys}
      />
    </>
  );
}
